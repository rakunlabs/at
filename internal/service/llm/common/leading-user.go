package common

// LeadingUserPlaceholder opens a conversation whose history begins with an
// assistant turn. Chat histories are loaded as a recency window and task
// chats can start from an imported assistant result, so the first retained
// message is not always a user message. Anthropic, Gemini and Bedrock
// reject such a request (a leading assistant tool call in particular), while
// OpenAI-shape providers accept it.
const LeadingUserPlaceholder = "[Earlier conversation omitted.]"
