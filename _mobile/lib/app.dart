import 'package:dio/dio.dart';
import 'package:flutter/material.dart';

import 'server.dart';
import 'auth.dart';
import 'auth_models.dart';
import 'chat_screens.dart';

class ATApp extends StatefulWidget {
  const ATApp({super.key, this.client, this.preferences, this.sessionFactory});
  final StatusClient? client;
  final ServerPreferences? preferences;
  final MobileSession Function(MobileAuth)? sessionFactory;

  @override
  State<ATApp> createState() => _ATAppState();
}

class _ATAppState extends State<ATApp> {
  late final client = widget.client ?? StatusClient();
  late final preferences = widget.preferences ?? ServerPreferences();
  late final credentials = SecureCredentialStore();

  @override
  void dispose() {
    if (widget.client == null) client.close();
    super.dispose();
  }

  ThemeData theme(Brightness brightness) {
    final dark = brightness == Brightness.dark;
    final scheme =
        ColorScheme.fromSeed(
          seedColor: const Color(0xff008a20),
          brightness: brightness,
        ).copyWith(
          primary: Color(dark ? 0xff55e870 : 0xff006d1b),
          onPrimary: Color(dark ? 0xff102014 : 0xffffffff),
          surface: Color(dark ? 0xff161618 : 0xfffafafa),
          onSurface: Color(dark ? 0xffe8e6e3 : 0xff1e1e20),
        );
    return ThemeData(
      useMaterial3: true,
      colorScheme: scheme,
      scaffoldBackgroundColor: scheme.surface,
      inputDecorationTheme: const InputDecorationTheme(
        border: OutlineInputBorder(),
        contentPadding: EdgeInsets.all(18),
      ),
      filledButtonTheme: FilledButtonThemeData(
        style: FilledButton.styleFrom(
          minimumSize: const Size(48, 52),
          padding: const EdgeInsets.symmetric(horizontal: 24, vertical: 16),
          shape: RoundedRectangleBorder(
            borderRadius: BorderRadius.circular(12),
          ),
        ),
      ),
      textButtonTheme: TextButtonThemeData(
        style: TextButton.styleFrom(minimumSize: const Size(48, 48)),
      ),
      textTheme: const TextTheme(
        headlineLarge: TextStyle(
          fontSize: 34,
          fontWeight: FontWeight.w700,
          height: 1.15,
        ),
        titleLarge: TextStyle(fontSize: 22, fontWeight: FontWeight.w600),
        bodyLarge: TextStyle(fontSize: 17, height: 1.5),
        bodyMedium: TextStyle(fontSize: 15, height: 1.5),
      ),
    );
  }

  @override
  Widget build(BuildContext context) => MaterialApp(
    title: 'AT',
    debugShowCheckedModeBanner: false,
    theme: theme(Brightness.light),
    darkTheme: theme(Brightness.dark),
    home: ConnectScreen(
      client: client,
      preferences: preferences,
      sessionFactory:
          widget.sessionFactory ??
          (discovery) => MobileSession(
            discovery,
            transport: client,
            store: credentials,
            browser: SystemLoginBrowser(),
          ),
    ),
  );
}

class ConnectScreen extends StatefulWidget {
  const ConnectScreen({
    super.key,
    required this.client,
    required this.preferences,
    required this.sessionFactory,
  });
  final StatusClient client;
  final ServerPreferences preferences;
  final MobileSession Function(MobileAuth) sessionFactory;

  @override
  State<ConnectScreen> createState() => _ConnectScreenState();
}

class _ConnectScreenState extends State<ConnectScreen> {
  final controller = TextEditingController();
  final form = GlobalKey<FormState>();
  CancelToken? request;
  String? message;
  bool loadingSaved = true;
  bool committing = false;
  bool forgetting = false;

  bool get busy => loadingSaved || request != null || committing || forgetting;

  @override
  void initState() {
    super.initState();
    restore();
  }

  Future<void> restore() async {
    try {
      final saved = await widget.preferences.load();
      if (mounted && saved != null) controller.text = saved;
    } catch (_) {
      if (mounted) {
        message =
            'Saved server could not be loaded. Enter its URL to continue.';
      }
    } finally {
      if (mounted) setState(() => loadingSaved = false);
    }
  }

  @override
  void dispose() {
    request?.cancel();
    controller.dispose();
    super.dispose();
  }

  void cancel() {
    if (committing || request == null) return;
    request?.cancel();
    setState(() {
      request = null;
      message = 'Verification cancelled.';
    });
  }

  Future<void> verify() async {
    if (busy || !form.currentState!.validate()) {
      return;
    }
    FocusScope.of(context).unfocus();
    final server = ServerAddress.parse(
      controller.text,
      allowLocalHttp: allowDevelopmentHttp,
    );
    final token = CancelToken();
    setState(() {
      request = token;
      message = null;
    });
    try {
      final status = await widget.client.verify(server, token);
      final mobile = status.mobileAuth == null
          ? null
          : MobileAuth.parse(status.mobileAuth, server);
      if (mobile != null && !status.enabled) throw invalidAuth;
      if (!mounted || token.isCancelled) return;
      // Preference writes cannot be cancelled once dispatched.
      setState(() => committing = true);
      String? saveWarning;
      try {
        await widget.preferences.save(server);
      } catch (_) {
        saveWarning = 'This server could not be saved on this device.';
      }
      if (!mounted || token.isCancelled) return;
      setState(() {
        request = null;
        committing = false;
        controller.text = server.toString();
      });
      final session = mobile == null ? null : widget.sessionFactory(mobile);
      await Navigator.of(context).push(
        MaterialPageRoute<void>(
          builder: (_) => session != null
              ? AccountScreen(session: session, saveWarning: saveWarning)
              : ServerScreen(
                  server: server,
                  status: status,
                  saveWarning: saveWarning,
                ),
        ),
      );
    } on VerificationFailure catch (e) {
      if (mounted && identical(request, token)) {
        setState(() => message = e.message);
      }
    } catch (_) {
      if (mounted && identical(request, token)) {
        setState(() => message = 'Verification failed. Please try again.');
      }
    } finally {
      if (mounted && identical(request, token)) {
        setState(() {
          request = null;
          committing = false;
        });
      }
    }
  }

  Future<void> forget() async {
    if (busy) return;
    FocusScope.of(context).unfocus();
    setState(() {
      forgetting = true;
      message = null;
    });
    try {
      await widget.preferences.clear();
      if (mounted) {
        setState(() {
          controller.clear();
          message = 'Saved server removed.';
        });
      }
    } catch (_) {
      if (mounted) {
        setState(
          () => message = 'Could not remove the saved server. Try again.',
        );
      }
    } finally {
      if (mounted) setState(() => forgetting = false);
    }
  }

  @override
  Widget build(BuildContext context) => PageShell(
    title: 'AT',
    children: [
      Text(
        'Your server.\nYour workspace.',
        style: Theme.of(context).textTheme.headlineLarge,
      ),
      const SizedBox(height: 16),
      const Text(
        'Connect to your self-hosted AT instance. Start with the address you use in your browser.',
        style: TextStyle(fontSize: 17, height: 1.5),
      ),
      const SizedBox(height: 36),
      Form(
        key: form,
        child: TextFormField(
          controller: controller,
          enabled: !busy,
          keyboardType: TextInputType.url,
          textInputAction: TextInputAction.go,
          autocorrect: false,
          enableSuggestions: false,
          decoration: const InputDecoration(
            labelText: 'Server URL',
            hintText: 'https://at.example.com/at/',
            helperText: 'Include the base path, if your server uses one.',
            helperMaxLines: 3,
            errorMaxLines: 4,
          ),
          validator: (value) {
            try {
              ServerAddress.parse(
                value ?? '',
                allowLocalHttp: allowDevelopmentHttp,
              );
            } on FormatException catch (e) {
              return e.message;
            }
            return null;
          },
          onFieldSubmitted: (_) => verify(),
        ),
      ),
      const SizedBox(height: 24),
      FilledButton(
        onPressed: !busy ? verify : null,
        child: Text(
          loadingSaved
              ? 'Loading saved server...'
              : committing
              ? 'Saving server...'
              : forgetting
              ? 'Removing saved server...'
              : request != null
              ? 'Verifying server...'
              : 'Verify server',
        ),
      ),
      if (request != null) ...[
        const SizedBox(height: 16),
        const LinearProgressIndicator(semanticsLabel: 'Connecting to server'),
        if (!committing)
          TextButton(
            onPressed: cancel,
            child: const Text('Cancel verification'),
          ),
      ],
      if (message != null) ...[
        const SizedBox(height: 16),
        Semantics(liveRegion: true, child: Text(message!)),
      ],
      const SizedBox(height: 32),
      const Divider(),
      const SizedBox(height: 16),
      Text(
        'A connection, not a sign-in',
        style: Theme.of(context).textTheme.titleMedium,
      ),
      const SizedBox(height: 8),
      const Text(
        'This checks public server capabilities only. No password, API token, or session is sent. This check saves only the server URL; signing in later stores mobile credentials in OS secure storage.',
      ),
      if (allowDevelopmentHttp) ...[
        const SizedBox(height: 16),
        const Text(
          'Development mode: HTTP is allowed for localhost, 127.0.0.1, and ::1 only.',
        ),
      ],
      const SizedBox(height: 16),
      TextButton(
        onPressed: !busy ? forget : null,
        child: const Text('Forget saved server'),
      ),
    ],
  );
}

class ServerScreen extends StatelessWidget {
  const ServerScreen({
    super.key,
    required this.server,
    required this.status,
    this.saveWarning,
  });
  final ServerAddress server;
  final AuthStatus status;
  final String? saveWarning;

  @override
  Widget build(BuildContext context) => PageShell(
    title: 'Server connection',
    children: [
      Icon(
        Icons.check_circle_outline,
        size: 40,
        color: Theme.of(context).colorScheme.primary,
        semanticLabel: 'Public server verification succeeded',
      ),
      const SizedBox(height: 20),
      Text('Server verified', style: Theme.of(context).textTheme.headlineLarge),
      const SizedBox(height: 12),
      SelectableText(
        server.toString(),
        style: Theme.of(context).textTheme.bodyLarge,
      ),
      const SizedBox(height: 8),
      const Text('Public status checked. You are not signed in to the app.'),
      if (saveWarning != null) ...[
        const SizedBox(height: 12),
        Text(saveWarning!),
      ],
      const SizedBox(height: 32),
      Text(
        'Web sign-in capabilities',
        style: Theme.of(context).textTheme.titleLarge,
      ),
      const SizedBox(height: 12),
      for (final row in <(String, String)>[
        ('Native web authentication', status.enabled ? 'Enabled' : 'Disabled'),
        ('Passkeys', status.passkeys ? 'Available' : 'Unavailable'),
        ('Remember me', status.rememberMe ? 'Available' : 'Unavailable'),
        ('Passkey login', status.passkeyLogin),
      ]) ...[
        const Divider(),
        Padding(
          padding: const EdgeInsets.symmetric(vertical: 10),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(row.$1, style: Theme.of(context).textTheme.titleMedium),
              const SizedBox(height: 4),
              Text(row.$2),
            ],
          ),
        ),
      ],
      const SizedBox(height: 24),
      Text(
        'Mobile sign-in not available yet',
        style: Theme.of(context).textTheme.titleLarge,
      ),
      const SizedBox(height: 12),
      const Text(
        'This server does not advertise mobile sign-in. Ask its operator to enable native authentication and deploy the Mobile PKCE Handoff V1 backend and browser approval page. Public verification alone does not establish a session.',
      ),
      const SizedBox(height: 16),
      const Text(
        'Continue in your browser at the address below. Browser sign-in will not sign in this app.',
      ),
      const SizedBox(height: 8),
      SelectableText(
        server.webLoginUrl.toString(),
        semanticsLabel: 'Web application address: ${server.webLoginUrl}',
      ),
      const SizedBox(height: 28),
      FilledButton(
        onPressed: () => Navigator.of(context).pop(),
        child: const Text('Change or recheck server'),
      ),
    ],
  );
}

class AccountScreen extends StatefulWidget {
  const AccountScreen({super.key, required this.session, this.saveWarning});
  final MobileSession session;
  final String? saveWarning;
  @override
  State<AccountScreen> createState() => _AccountScreenState();
}

class _AccountScreenState extends State<AccountScreen>
    with WidgetsBindingObserver {
  bool busy = true;
  bool remember = false;
  bool leaving = false;
  String? message;
  MobileSession get session => widget.session;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    session.addListener(changed);
    run(session.restore);
  }

  void changed() {
    if (mounted) setState(() {});
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed &&
        !busy &&
        session.metadata != null) {
      run(session.checkAccount);
    }
  }

  Future<void> run(Future<void> Function() operation) async {
    setState(() {
      busy = true;
      message = null;
    });
    try {
      await operation();
    } on VerificationFailure catch (e) {
      if (mounted) message = e.message;
    } catch (_) {
      if (mounted) {
        message = 'The operation did not complete. Please try again.';
      }
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  Future<void> signOut({bool switchServer = false}) async {
    if (busy) return;
    await run(() async {
      final result = await session.logout();
      if (!mounted) return;
      message = result;
      if (switchServer) {
        // Carry revocation uncertainty back to the server selector, not a false success.
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text(result)));
        setState(() => leaving = true);
        WidgetsBinding.instance.addPostFrameCallback((_) {
          if (mounted) Navigator.of(context).pop();
        });
      }
    });
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    session.removeListener(changed);
    session.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final account = session.account;
    final tokens = session.metadata;
    return PopScope(
      canPop: leaving,
      onPopInvokedWithResult: (didPop, _) {
        if (!didPop && !busy) signOut(switchServer: true);
      },
      child: PageShell(
        title: 'AT account',
        children: [
          Text(
            account == null ? 'Sign in to AT' : 'Your account',
            style: Theme.of(context).textTheme.headlineLarge,
          ),
          const SizedBox(height: 12),
          SelectableText(session.discovery.server.toString()),
          if (widget.saveWarning != null) ...[
            const SizedBox(height: 12),
            Text(widget.saveWarning!),
          ],
          const SizedBox(height: 28),
          if (account != null && tokens != null) ...[
            Text(account.name, style: Theme.of(context).textTheme.titleLarge),
            const SizedBox(height: 8),
            const Text('Identity verified by this server.'),
            const SizedBox(height: 16),
            SelectableText('Subject: ${account.subject}'),
            Text(
              account.roles.contains('admin')
                  ? 'Role: Administrator'
                  : 'Role: Account holder',
            ),
            const Divider(height: 32),
            Text('Access expires: ${tokens.accessExpires.toLocal()}'),
            Text('Session ends: ${tokens.sessionExpires.toLocal()}'),
            Text(
              tokens.rememberMe
                  ? 'Extended session requested'
                  : 'Standard session requested',
            ),
            const SizedBox(height: 16),
            if (account.roles.contains('admin'))
              FilledButton.icon(
                onPressed: busy
                    ? null
                    : () => Navigator.of(context).push(
                        MaterialPageRoute<void>(
                          builder: (_) => ConversationsScreen(session: session),
                        ),
                      ),
                icon: const Icon(Icons.chat_bubble_outline),
                label: const Text('Personal conversations'),
              )
            else
              const Text(
                'Personal conversations are administrator-only. This account is not authorized; account and session management remain available.',
              ),
          ] else if (!busy && tokens == null) ...[
            const Text(
              'Use your server\'s password or passkey in the system browser, then explicitly approve AT Mobile. Your password never enters this app.',
            ),
            const SizedBox(height: 16),
            CheckboxListTile(
              contentPadding: EdgeInsets.zero,
              value: remember,
              onChanged: busy
                  ? null
                  : (value) => setState(() => remember = value!),
              title: const Text('Remember this device'),
              subtitle: const Text(
                'Off: server default, usually 8 hours (at most 24 hours). On: up to 30 days. The server sets the final expiry.',
              ),
              controlAffinity: ListTileControlAffinity.leading,
            ),
            const SizedBox(height: 16),
            FilledButton(
              onPressed: busy
                  ? null
                  : () => run(() => session.login(rememberMe: remember)),
              child: const Text('Sign in with browser'),
            ),
          ],
          if (tokens != null) ...[
            const SizedBox(height: 24),
            FilledButton(
              onPressed: busy ? null : () => run(session.checkAccount),
              child: const Text('Check account'),
            ),
          ],
          if (busy) ...[
            const SizedBox(height: 24),
            const LinearProgressIndicator(
              semanticsLabel: 'Account operation in progress',
            ),
            const SizedBox(height: 12),
            Semantics(
              liveRegion: true,
              child: Text(
                session.phase.isEmpty ? 'Please wait...' : session.phase,
              ),
            ),
            const SizedBox(height: 8),
            const Text(
              'You can cancel browser sign-in using the browser\'s Close or Back control. Secure session writes cannot be cancelled.',
            ),
          ],
          if (message != null) ...[
            const SizedBox(height: 20),
            Semantics(liveRegion: true, child: Text(message!)),
          ],
          const SizedBox(height: 24),
          TextButton(
            onPressed: busy ? null : () => signOut(),
            child: const Text('Sign out and remove local session'),
          ),
          TextButton(
            onPressed: busy ? null : () => signOut(switchServer: true),
            child: const Text('Sign out and change server'),
          ),
          const SizedBox(height: 16),
          const Text(
            'Mobile credentials stay in OS secure storage for this installation and server only. Signing out here does not sign out your browser.',
          ),
        ],
      ),
    );
  }
}

class PageShell extends StatelessWidget {
  const PageShell({super.key, required this.title, required this.children});
  final String title;
  final List<Widget> children;

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: Text(title)),
    body: SafeArea(
      child: Align(
        alignment: Alignment.topCenter,
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 560),
          child: SingleChildScrollView(
            keyboardDismissBehavior: ScrollViewKeyboardDismissBehavior.onDrag,
            padding: const EdgeInsets.fromLTRB(24, 28, 24, 32),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: children,
            ),
          ),
        ),
      ),
    ),
  );
}
