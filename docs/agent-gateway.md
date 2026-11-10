# Embedding an AT agent in a website

A website can put an AT agent behind its own chat widget. AT keeps the
system prompt, skills, MCP servers, model routing, guardrails and traces.
Every turn runs **as the signed-in person**. That gives you:

- their workspace access and permission mappings, so they reach only the MCP
  servers they are allowed to;
- their own connected accounts (Jira, Confluence or GitLab through MCP OAuth);
- their budget, and their user ID in Traces and Usage.

```
Browser (chat widget) ──► Website backend ──► AT  POST /gateway/v1/agents/{agent}/chat
  person's session          AT API token          verifies the person's IdP token
                            + person's IdP token   runs the agent as that person
```

The AT token stays in the backend. It never reaches the browser.

## 1. Use the same identity provider

Add the website's identity provider (Keycloak, Entra ID, Okta, …) under
**Settings → Authentication → Identity providers**, or edit the existing one.
In **Token exchange (agent gateway)**:

| Field | Value |
|---|---|
| Accept this provider's tokens from trusted backends | on |
| JWKS URL | the provider's signing keys, e.g. `https://sso.example.com/realms/company/protocol/openid-connect/certs` |
| Issuer | the exact `iss` of the tokens, e.g. `https://sso.example.com/realms/company` |
| Accepted audiences | the `aud` the website's tokens carry, usually its client ID |
| Create an account on first use | on, if people should not have to open AT once first |

The **allowed email addresses** and **role claim paths** of the provider apply
exactly as at a browser sign-in. For Keycloak, `realm_access.roles` or a
`groups` mapper is the usual choice.

## 2. Decide who may use what

Under **Settings → Permissions → Mappings**, map the provider's groups or roles
to the workspace:

- **Admission role** (e.g. `member`): a person whose token carries the group
  becomes a workspace member on first use. People without the group get
  `403 no_workspace_access`.
- **Permission bundle**: what that group may do, for example `mcp.use` on the
  Jira and Confluence MCP sets.

Revoking someone's group takes effect on their next request. Disabling their
account, removing their membership or pausing the token stops a run at its
next action.

## 3. Create the agent and the token

1. Create the agent (system prompt, skills, MCP sets, model or routing
   profile) under **Agents**. Upstreams that sign in per user (MCP OAuth with
   the *Signed-in user* source) use each person's own connection.
2. Under **Settings → API tokens**, create a workspace token, turn on
   **Act for users** (workspace administrators only), and list the chatbot's
   agent under its agents. Token limits, spend limits and guardrail policies
   that name this token apply to every person it acts for.

## 4. Call the endpoint from the backend

```http
POST /gateway/v1/agents/shop-assistant/chat
Authorization: Bearer at_…            (the AT token, backend only)
Content-Type: application/json

{
  "subject_token": "<the person's access token or id_token from the IdP>",
  "message": "What is in my cart?",
  "session_id": "",                    // empty starts a conversation
  "client_tools": [ … ]                // optional, see below
}
```

`{agent}` is the agent's ID or name. `identity_provider` names the AT provider
ID; you can leave it out when only one provider has token exchange on.

The response is `text/event-stream`. Each frame has an event name:

| Event | Data |
|---|---|
| `session` | `{session_id, user_id, agent_id}`. Keep `session_id` to continue |
| `message` | `{content, final}`: assistant text; `final: true` is the answer |
| `tool_call` / `tool_progress` / `tool_result` | server-side tool activity, for "searching Jira…" indicators |
| `client_tool_calls` | `{session_id, calls: [{tool_call_id, name, arguments}]}`: run these yourself |
| `error` | `{message}` |
| `done` | `{session_id, status}`, where status is `completed`, `requires_action` or `failed` |

Send `session_id` to continue a conversation. A session belongs to the
person, the token and the agent; anyone else gets 404. The conversation also
appears in the person's own AT **Sessions** list.

Errors before the stream starts use the OpenAI envelope
`{"error":{"message","code"}}`:

| Status | Code |
|---|---|
| 401 | `invalid_api_key`, `invalid_subject_token` |
| 403 | `act_for_users_required`, `no_workspace_access`, `unknown_user` |
| 404 | `agent_not_found`, `invalid_session` |
| 409 | `tool_results_required`, `invalid_tool_results` |
| 429 | `token_limit_exceeded` |

Guardrail blocks arrive as an `error` event.

## 5. Tools that run in the website: `client_tools`

The website can offer its own tools, such as "read the cart", "open a page" or
"fill a form". They are written in JavaScript, run in the backend or the
browser, and act as the person in the website's own session. AT shows them to
the model next to the agent's server tools but never runs them:

```json
"client_tools": [{
  "name": "get_cart",
  "description": "Read the shopping cart of the current visitor",
  "input_schema": { "type": "object", "properties": { "currency": { "type": "string" } } }
}]
```

When the model calls one, the stream ends with `client_tool_calls` and
`done` with status `requires_action`. Run the calls and continue the same
session with every result:

```json
{
  "subject_token": "…",
  "session_id": "01J…",
  "client_tools": [ … ],
  "tool_results": [{ "tool_call_id": "call_cart", "content": "{\"items\":2}" }]
}
```

Rules:

- Send `client_tools` on every request. The model only sees the tools sent
  with that turn.
- Names use letters, digits, `_` and `-`. A name already used by a server
  tool is ignored, so a page cannot replace one of the agent's tools.
- A new `message` is refused with 409 while calls are open: answer them
  first. You can send a `message` in the same request as `tool_results`.
- Set `is_error: true` for a failed call, and the model will see the error.

Client tools carry no AT authority. They run wherever your code runs, with
whatever rights it has. AT does not store their definitions. It stores their
calls and results in the conversation, and those pass through guardrails like
any other tool result.

## 6. Browser MCP tools (WebMCP, extensions)

A page can also hand the agent tools the browser exposes. One kind is
WebMCP tools registered by the page itself through `navigator.modelContext`.
Another kind is a browser extension that speaks AT's extension bridge. AT
never reaches the browser directly, so the widget relays these tools:

1. **Collect** the tools in the page: `navigator.modelContext` tools, or
   `tools/list` from an extension through the
   [`at.extension.bridge` postMessage protocol](../AGENTS.md#browser-extensions-in-chats).
2. **Send** them with the message, as `client_tools`, from the page through
   your backend. Convert each tool's MCP `inputSchema` to `input_schema`.
3. **Execute** each call in `client_tool_calls` in the page through the same
   API (`tools/call` for an extension), and send the outputs back as
   `tool_results`.

A minimal widget loop:

```js
async function turn(body) {
  const res = await fetch('/api/chat', { method: 'POST', body: JSON.stringify(body) }); // your backend
  let session, calls = [];
  for await (const { event, data } of readSSE(res.body)) {
    if (event === 'session') session = data.session_id;
    if (event === 'message') render(data.content, data.final);
    if (event === 'client_tool_calls') calls = data.calls;
  }
  if (calls.length) {
    const tool_results = await Promise.all(calls.map(async c => ({
      tool_call_id: c.tool_call_id,
      content: JSON.stringify(await browserTools.call(c.name, c.arguments)),
    })));
    return turn({ session_id: session, tool_results, client_tools: browserTools.list() });
  }
  return session;
}
```

Treat browser tools like any third-party code. A page with an injected script,
or a malicious extension, can return anything. Let the person approve them,
for example with a "this page may use these tools" prompt. Turn on a
guardrail policy with **tool results: withhold** for the token, so
instructions hidden in a tool result do not steer the agent.

## Security notes

- The identity is proven only by the IdP token: signature, issuer, audience
  and expiry are all checked. AT never reads a user name from a header or
  from the request body, and an act-for-users token cannot pick who it acts
  for.
- Keep the AT token in the backend. Anyone holding it, together with a valid
  person token for the configured audience, can run the token's agents as
  that person.
- A person's token is accepted for one request, not stored. Send it on every
  request; refresh it as the IdP requires.
- Tools that need confirmation (`confirmation_required_tools`) cannot be
  approved through the gateway. They are refused at once, and the model is
  told the tool needs approval in AT. Do not list a tool there if the website
  must use it, or make it a client tool and ask the person in the widget.
