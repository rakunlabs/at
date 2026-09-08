import 'package:dio/dio.dart';
import 'package:flutter/material.dart';

import 'app.dart';
import 'auth.dart';
import 'chat.dart';
import 'chat_models.dart';

class ConversationsScreen extends StatefulWidget {
  const ConversationsScreen({super.key, required this.session});
  final MobileSession session;
  @override
  State<ConversationsScreen> createState() => _ConversationsScreenState();
}

class _ConversationsScreenState extends State<ConversationsScreen> {
  late final api = PersonalChatApi(widget.session);
  final token = CancelToken();
  final items = <Conversation>[];
  String nextBefore = '';
  bool busy = false;
  String? error;
  @override
  void initState() {
    super.initState();
    widget.session.addListener(sessionChanged);
    load();
  }

  void sessionChanged() {
    if (!api.live && mounted) {
      token.cancel();
      setState(() {
        items.clear();
        api.pendingTurns.clear();
        error = 'Session ended or not authorized. Return to Account.';
      });
    }
  }

  Future<void> load({bool older = false}) async {
    if (busy || !api.live || (older && nextBefore.isEmpty)) return;
    setState(() {
      busy = true;
      error = null;
    });
    try {
      final page = await api.conversations(
        before: older ? nextBefore : null,
        cancel: token,
      );
      if (!mounted || !api.live) return;
      setState(() {
        if (!older) items.clear();
        for (final item in page.items) {
          if (!items.any((v) => v.id == item.id)) items.add(item);
        }
        nextBefore = page.nextBefore;
      });
    } catch (e) {
      if (mounted) setState(() => error = chatFailure(e));
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> open(Conversation conversation) async {
    if (!api.live || busy) return;
    final controller = ConversationController(api, conversation);
    await Navigator.of(context).push(
      MaterialPageRoute<void>(
        builder: (_) => ConversationScreen(controller: controller),
      ),
    );
    if (mounted) await load();
  }

  Future<void> create() async {
    if (busy || !api.live) return;
    final conversation = await Navigator.of(context).push<Conversation>(
      MaterialPageRoute(builder: (_) => NewConversationScreen(api: api)),
    );
    if (!mounted || !api.live) return;
    if (conversation != null) {
      await open(conversation);
    } else {
      await load();
    }
  }

  Future<void> change(Conversation conversation, String action) async {
    if (busy || !api.live) return;
    String? title;
    if (action == 'rename') {
      var draft = conversation.title;
      title = await showDialog<String>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('Rename conversation'),
          content: TextFormField(
            initialValue: draft,
            onChanged: (value) => draft = value,
            autofocus: true,
            decoration: const InputDecoration(
              labelText: 'Title',
              helperText: 'Up to 256 UTF-8 bytes',
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context),
              child: const Text('Cancel'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(context, draft),
              child: const Text('Rename'),
            ),
          ],
        ),
      );
      if (title == null) return;
    } else {
      final confirm = await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: const Text('Delete conversation?'),
          content: const Text(
            'This permanently deletes the conversation and all its messages on the server.',
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('Keep conversation'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(context, true),
              child: const Text('Delete'),
            ),
          ],
        ),
      );
      if (confirm != true) return;
    }
    if (!mounted || !api.live) return;
    setState(() {
      busy = true;
      error = null;
    });
    try {
      if (title != null) {
        await api.rename(conversation.id, title, cancel: token);
      } else {
        await api.delete(conversation.id, cancel: token);
      }
    } catch (e) {
      if (mounted) setState(() => error = chatFailure(e));
    } finally {
      if (mounted) setState(() => busy = false);
    }
    if (mounted && error == null) await load();
  }

  @override
  void dispose() {
    token.cancel();
    items.clear();
    api.pendingTurns.clear();
    widget.session.removeListener(sessionChanged);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      title: const Text('Conversations'),
      actions: [
        IconButton(
          tooltip: 'Reload conversations',
          onPressed: busy || !api.live ? null : load,
          icon: const Icon(Icons.refresh),
        ),
      ],
    ),
    body: SafeArea(
      child: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 760),
          child: !api.live
              ? const Padding(
                  padding: EdgeInsets.all(24),
                  child: Text(
                    'Personal conversations require an administrator account. Return to Account.',
                  ),
                )
              : ListView(
                  padding: const EdgeInsets.all(20),
                  children: [
                    Text(
                      'Your conversations',
                      style: Theme.of(context).textTheme.headlineLarge,
                    ),
                    const SizedBox(height: 12),
                    const Text(
                      'Saved on your AT server. Continue the same conversation on web or mobile.',
                    ),
                    const SizedBox(height: 20),
                    FilledButton.icon(
                      onPressed: busy ? null : create,
                      icon: const Icon(Icons.add),
                      label: const Text('New conversation'),
                    ),
                    if (busy) ...[
                      const SizedBox(height: 16),
                      const LinearProgressIndicator(
                        semanticsLabel: 'Loading conversations',
                      ),
                    ],
                    if (error != null) ...[
                      const SizedBox(height: 16),
                      Semantics(liveRegion: true, child: Text(error!)),
                    ],
                    if (!busy && items.isEmpty && error == null)
                      const Padding(
                        padding: EdgeInsets.symmetric(vertical: 36),
                        child: Text(
                          'No conversations yet. Choose a model and start with a question.',
                        ),
                      ),
                    const SizedBox(height: 20),
                    for (final item in items) ...[
                      ListTile(
                        contentPadding: EdgeInsets.zero,
                        title: Text(item.title),
                        subtitle: Text('${item.provider} / ${item.model}'),
                        onTap: busy ? null : () => open(item),
                        trailing: PopupMenuButton<String>(
                          tooltip: 'Options for ${item.title}',
                          enabled: !busy,
                          onSelected: (value) => change(item, value),
                          itemBuilder: (_) => const [
                            PopupMenuItem(
                              value: 'rename',
                              child: Text('Rename'),
                            ),
                            PopupMenuItem(
                              value: 'delete',
                              child: Text('Delete'),
                            ),
                          ],
                        ),
                      ),
                      const Divider(),
                    ],
                    if (nextBefore.isNotEmpty)
                      TextButton(
                        onPressed: busy ? null : () => load(older: true),
                        child: const Text('Load older conversations'),
                      ),
                  ],
                ),
        ),
      ),
    ),
  );
}

class NewConversationScreen extends StatefulWidget {
  const NewConversationScreen({super.key, required this.api});
  final PersonalChatApi api;
  @override
  State<NewConversationScreen> createState() => _NewConversationScreenState();
}

class _NewConversationScreenState extends State<NewConversationScreen> {
  final title = TextEditingController(text: 'New conversation');
  final prompt = TextEditingController();
  final token = CancelToken();
  List<ChatModel> models = [];
  ChatModel? selected;
  bool busy = true;
  String? error;
  @override
  void initState() {
    super.initState();
    widget.api.session.addListener(changed);
    load();
  }

  void changed() {
    if (!widget.api.live && mounted) {
      token.cancel();
      title.clear();
      prompt.clear();
      setState(() {
        models.clear();
        selected = null;
      });
    }
  }

  Future<void> load() async {
    try {
      final result = await widget.api.models(cancel: token);
      if (mounted && widget.api.live) setState(() => models = result);
    } catch (e) {
      if (mounted) setState(() => error = chatFailure(e));
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> chooseModel() async {
    final result = await showModalBottomSheet<ChatModel>(
      context: context,
      isScrollControlled: true,
      builder: (context) => SafeArea(
        child: SizedBox(
          height: MediaQuery.sizeOf(context).height * .7,
          child: ListView(
            padding: const EdgeInsets.all(20),
            children: [
              Text(
                'Choose a model',
                style: Theme.of(context).textTheme.titleLarge,
              ),
              for (final model in models)
                ListTile(
                  title: Text(model.model),
                  subtitle: Text(model.provider),
                  onTap: () => Navigator.pop(context, model),
                ),
            ],
          ),
        ),
      ),
    );
    if (mounted && widget.api.live && result != null) {
      setState(() => selected = result);
    }
  }

  Future<void> create() async {
    if (busy || selected == null || !widget.api.live) return;
    try {
      chatText(title.text, 256);
      chatText(prompt.text, 32768, blank: true);
    } catch (_) {
      setState(
        () => error = 'Enter a nonblank title up to 256 UTF-8 bytes and instructions up to 32 KiB, without NUL characters.',
      );
      return;
    }
    setState(() {
      busy = true;
      error = null;
    });
    try {
      final conversation = await widget.api.create(
        title.text,
        selected!,
        prompt.text,
        cancel: token,
      );
      if (mounted && widget.api.live) Navigator.pop(context, conversation);
    } catch (e) {
      if (mounted) {
        setState(
          () => error =
              '${chatFailure(e)} Creation may have succeeded; go back and reload the list before creating again.',
        );
      }
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  @override
  void dispose() {
    token.cancel();
    title.dispose();
    prompt.dispose();
    widget.api.session.removeListener(changed);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => PageShell(
    title: 'New conversation',
    children: [
      if (!widget.api.live)
        const Text('Personal conversations require an administrator account.')
      else ...[
        const Text(
          'Choose a configured model. Conversations are text-only; model availability is determined by your server.',
        ),
        const SizedBox(height: 24),
        TextField(
          controller: title,
          enabled: !busy,
          decoration: const InputDecoration(labelText: 'Conversation title'),
        ),
        const SizedBox(height: 20),
        OutlinedButton(
          onPressed: busy || models.isEmpty ? null : chooseModel,
          child: Text(selected?.label ?? 'Choose provider and model'),
        ),
        if (!busy && models.isEmpty)
          const Text(
            'No configured chat models are available. Ask your server operator to configure one.',
          ),
        const SizedBox(height: 20),
        TextField(
          controller: prompt,
          enabled: !busy,
          minLines: 3,
          maxLines: 8,
          decoration: const InputDecoration(
            labelText: 'System instructions (optional)',
            helperText: 'Up to 32 KiB of UTF-8 text',
            helperMaxLines: 2,
          ),
        ),
        const SizedBox(height: 24),
        FilledButton(
          onPressed: busy || selected == null ? null : create,
          child: Text(busy ? 'Please wait...' : 'Create conversation'),
        ),
        if (busy)
          const Padding(
            padding: EdgeInsets.only(top: 16),
            child: LinearProgressIndicator(),
          ),
        if (error != null)
          Padding(
            padding: const EdgeInsets.only(top: 16),
            child: Semantics(liveRegion: true, child: Text(error!)),
          ),
      ],
    ],
  );
}

class ConversationScreen extends StatefulWidget {
  const ConversationScreen({super.key, required this.controller});
  final ConversationController controller;
  @override
  State<ConversationScreen> createState() => _ConversationScreenState();
}

class _ConversationScreenState extends State<ConversationScreen> {
  final composer = TextEditingController();
  final scroll = ScrollController();
  String? inputError;
  ConversationController get chat => widget.controller;
  @override
  void initState() {
    super.initState();
    chat.addListener(changed);
    chat.load();
  }

  void changed() {
    if (!mounted) return;
    final follow = scroll.hasClients && scroll.position.extentAfter < 100;
    if (!chat.live) composer.clear();
    setState(() {});
    if (follow) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted && scroll.hasClients) {
          scroll.jumpTo(scroll.position.maxScrollExtent);
        }
      });
    }
  }

  Future<void> send() async {
    if (!chat.canSend) return;
    final content = composer.text;
    try {
      chatText(content, 32768);
    } catch (_) {
      setState(
        () => inputError = 'Write a message of 1 to 32,768 UTF-8 bytes, without NUL characters.',
      );
      return;
    }
    setState(() {
      inputError = null;
      composer.clear();
    });
    await chat.send(content);
  }

  @override
  void dispose() {
    chat.removeListener(changed);
    chat.dispose();
    composer.dispose();
    scroll.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(
      title: Text(chat.live ? chat.conversation.title : 'Conversation'),
      actions: [
        IconButton(
          tooltip: 'Go to latest message',
          onPressed: () {
            if (scroll.hasClients) {
              scroll.jumpTo(scroll.position.maxScrollExtent);
            }
          },
          icon: const Icon(Icons.vertical_align_bottom),
        ),
      ],
    ),
    body: SafeArea(
      child: Center(
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 760),
          child: !chat.live
              ? const Padding(
                  padding: EdgeInsets.all(24),
                  child: Text(
                    'Session ended or not authorized. Return to Account.',
                  ),
                )
              : LayoutBuilder(
                  builder: (context, available) => Column(
                    children: [
                      Expanded(
                        child: ListView.builder(
                          controller: scroll,
                          padding: const EdgeInsets.all(20),
                          itemCount: chat.messages.length + 1,
                          itemBuilder: (context, index) {
                            if (index == 0) {
                              return Column(
                                crossAxisAlignment: CrossAxisAlignment.stretch,
                                children: [
                                  Text(
                                    '${chat.conversation.provider} / ${chat.conversation.model}',
                                    style: Theme.of(context)
                                        .textTheme
                                        .labelLarge,
                                  ),
                                  const SizedBox(height: 12),
                                  const Text(
                                    'Saved on your server. Leaving this screen disconnects an active reply; its last saved partial text remains.',
                                  ),
                                  if (chat.loading)
                                    const LinearProgressIndicator(
                                      semanticsLabel: 'Loading saved messages',
                                    ),
                                  if (chat.nextBefore.isNotEmpty)
                                    TextButton(
                                      onPressed: chat.loading
                                          ? null
                                          : () => chat.load(older: true),
                                      child: const Text('Load older messages'),
                                    ),
                                  if (!chat.loading && chat.messages.isEmpty)
                                    const Padding(
                                      padding: EdgeInsets.symmetric(
                                        vertical: 32,
                                      ),
                                      child: Text(
                                        'Start this conversation with a question.',
                                      ),
                                    ),
                                  const SizedBox(height: 24),
                                ],
                              );
                            }
                            final message = chat.messages[index - 1];
                            return Padding(
                              key: ValueKey(message.id),
                              padding: const EdgeInsets.only(bottom: 28),
                              child: Column(
                                crossAxisAlignment: CrossAxisAlignment.stretch,
                                children: [
                                  Text(
                                    message.role == 'user'
                                        ? 'You'
                                        : message.model,
                                    style: Theme.of(context)
                                        .textTheme
                                        .titleMedium,
                                  ),
                                  const SizedBox(height: 8),
                                  SelectableText(
                                    message.content.isEmpty
                                        ? message.terminal
                                              ? 'No text was returned.'
                                              : 'Waiting for a reply...'
                                        : message.content,
                                    style: Theme.of(context)
                                        .textTheme
                                        .bodyLarge,
                                  ),
                                  if (message.role == 'assistant') ...[
                                    const SizedBox(height: 10),
                                    Text(message.statusLabel),
                                    if (message.terminal)
                                      Text(
                                        '${message.usage['total_tokens']} tokens reported',
                                        style: Theme.of(context)
                                            .textTheme
                                            .bodySmall,
                                      ),
                                  ],
                                  const SizedBox(height: 16),
                                  const Divider(),
                                ],
                              ),
                            );
                          },
                        ),
                      ),
                      ConstrainedBox(
                        constraints: BoxConstraints(
                          maxHeight: available.maxHeight * .55,
                        ),
                        child: SingleChildScrollView(
                          padding: const EdgeInsets.fromLTRB(20, 8, 20, 16),
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.stretch,
                            children: [
                              if (chat.error != null)
                                Semantics(
                                  liveRegion: true,
                                  child: Text(chat.error!),
                                ),
                              if (!chat.ready && !chat.loading)
                                TextButton(
                                  onPressed: chat.load,
                                  child: const Text('Reload conversation'),
                                ),
                              if (chat.pending != null &&
                                  chat.activeId == null &&
                                  !chat.sending) ...[
                                Text(
                                  'Unconfirmed message: ${chat.pending!.content}',
                                  maxLines: 3,
                                  overflow: TextOverflow.ellipsis,
                                ),
                                TextButton(
                                  onPressed: chat.recovering
                                      ? null
                                      : () => chat.send('', replay: true),
                                  child: const Text('Recover same send'),
                                ),
                              ],
                              if (chat.activeId != null)
                                Wrap(
                                  spacing: 8,
                                  children: [
                                    TextButton(
                                      onPressed: chat.cancelling
                                          ? null
                                          : chat.cancelReply,
                                      child: Text(
                                        chat.cancelling
                                            ? 'Cancelling...'
                                            : 'Cancel reply',
                                      ),
                                    ),
                                    if (!chat.sending)
                                      TextButton(
                                        onPressed: chat.cancelling
                                            ? null
                                            : () => chat.recover(restart: true),
                                        child: const Text('Check saved reply'),
                                      ),
                                  ],
                                ),
                              if (chat.sending || chat.recovering)
                                const LinearProgressIndicator(
                                  semanticsLabel:
                                      'Waiting for saved reply status',
                                ),
                              const SizedBox(height: 8),
                              TextField(
                                controller: composer,
                                enabled: chat.canSend,
                                minLines: 1,
                                maxLines: 6,
                                keyboardType: TextInputType.multiline,
                                decoration: InputDecoration(
                                  labelText: 'Message',
                                  errorText: inputError,
                                  errorMaxLines: 4,
                                  helperText: 'Text only, up to 32 KiB. Replies may incur provider costs.',
                                  helperMaxLines: 3,
                                ),
                              ),
                              const SizedBox(height: 12),
                              FilledButton.icon(
                                onPressed: chat.canSend ? send : null,
                                icon: const Icon(Icons.arrow_upward),
                                label: const Text('Send message'),
                              ),
                            ],
                          ),
                        ),
                      ),
                    ],
                  ),
                ),
        ),
      ),
    ),
  );
}
