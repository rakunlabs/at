import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:crypto/crypto.dart';
import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:flutter_secure_storage/flutter_secure_storage.dart';
import 'package:flutter_web_auth_2/flutter_web_auth_2.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'auth_models.dart';
import 'server.dart';

abstract class LoginBrowser {
  Future<String> open(Uri url);
}

class SystemLoginBrowser implements LoginBrowser {
  @override
  Future<String> open(Uri url) async {
    if (!Platform.isAndroid && !Platform.isIOS) {
      throw const VerificationFailure(
        'Mobile sign-in requires iOS or Android.',
      );
    }
    try {
      return await FlutterWebAuth2.authenticate(
        url: url.toString(),
        callbackUrlScheme: 'atmobile',
        options: const FlutterWebAuth2Options(useWebview: false),
      );
    } on PlatformException catch (e) {
      throw VerificationFailure(
        e.code == 'CANCELED'
            ? 'Browser sign-in cancelled.'
            : 'Could not open browser sign-in. Please try again.',
      );
    }
  }
}

abstract class CredentialStore {
  Future<String?> read(String issuer);
  Future<void> write(String issuer, String value);
  Future<void> delete(String issuer);
}

class RefreshDeferred extends VerificationFailure {
  const RefreshDeferred()
    : super('Refresh is rate limited. Wait before checking the account again.');
}

class SecureCredentialStore implements CredentialStore {
  final _storage = const FlutterSecureStorage(
    iOptions: IOSOptions(
      accessibility: KeychainAccessibility.unlocked_this_device,
      synchronizable: false,
    ),
  );
  late final Future<String> _installation = _installationId();

  Future<String> _installationId() async {
    final prefs = SharedPreferencesAsync();
    var id = await prefs.getString('at.installation_id');
    if (!canonicalSecret(id)) {
      id = randomSecret();
      await prefs.setString('at.installation_id', id);
    }
    return id!;
  }

  // The nonsecret installation marker makes surviving iOS Keychain entries
  // unreachable after reinstall. No credential is ever placed in preferences.
  Future<String> key(String issuer) async =>
      'at.mobile.v1.${await _installation}.${sha256.convert(utf8.encode(issuer))}';
  @override
  Future<String?> read(String issuer) async =>
      _storage.read(key: await key(issuer));
  @override
  Future<void> write(String issuer, String value) async {
    final name = await key(issuer);
    await _storage.write(key: name, value: value);
    if (await _storage.read(key: name) != value) {
      throw const VerificationFailure(
        'Secure storage could not confirm the session write.',
      );
    }
  }

  @override
  Future<void> delete(String issuer) async {
    final name = await key(issuer);
    await _storage.delete(key: name);
    if (await _storage.read(key: name) != null) {
      throw const VerificationFailure(
        'Secure storage could not remove the session.',
      );
    }
  }
}

class MobileSession extends ChangeNotifier {
  MobileSession(
    this.discovery, {
    required this.transport,
    required this.store,
    required this.browser,
    DateTime Function()? clock,
  }) : now = clock ?? DateTime.now;
  final MobileAuth discovery;
  final StatusClient transport;
  final CredentialStore store;
  final LoginBrowser browser;
  final DateTime Function() now;
  MobileTokens? _tokens;
  AccountIdentity? account;
  MobileTokens? get metadata => _tokens;
  String phase = '';
  int _generation = 0;
  bool _disposed = false;
  bool _storageBlocked = false;
  Future<void> _queue = Future.value();
  Future<void>? _refreshFlight;
  DateTime? _retryAt;
  final _activeRequests = <CancelToken>{};
  int get generation => _generation;
  bool get canChat =>
      !_disposed &&
      account?.roles.contains('admin') == true &&
      _tokens != null &&
      _tokens!.sessionExpires.isAfter(now());

  void _cancelRequests() {
    for (final token in _activeRequests.toList()) {
      token.cancel('session ended');
    }
    _activeRequests.clear();
  }

  void _emit(String value) {
    phase = value;
    if (!_disposed) notifyListeners();
  }

  void _check(int generation) {
    if (_disposed || generation != _generation) {
      throw const VerificationFailure('Session changed. Start sign-in again.');
    }
  }

  Future<T> _serial<T>(Future<T> Function() action) {
    final result = _queue.then((_) => action());
    _queue = result.then<void>((_) {}, onError: (Object _, StackTrace _) {});
    return result;
  }

  Future<void> _clear() async {
    _cancelRequests();
    _tokens = null;
    account = null;
    if (!_disposed) notifyListeners();
    try {
      await store.delete(discovery.server.issuer);
    } catch (_) {
      // If deletion fails, overwrite with a credential-free tombstone.
      try {
        await store.write(discovery.server.issuer, '{}');
      } catch (_) {
        _storageBlocked = true;
        throw const VerificationFailure(
          'Secure session cleanup failed. Unlock the device and retry sign-out before signing in.',
        );
      }
    }
  }

  Future<void> _commit(MobileTokens tokens, int generation) async {
    _check(generation);
    _emit('Saving secure session...');
    try {
      await store.write(discovery.server.issuer, jsonEncode(tokens.json));
      _check(generation);
      _tokens = tokens;
    } catch (_) {
      await _clear();
      throw const VerificationFailure(
        'The session could not be securely saved. Sign in again.',
      );
    }
  }

  Future<void> restore() {
    final generation = _generation;
    return _serial(() async {
      _check(generation);
      if (_storageBlocked) {
        throw const VerificationFailure(
          'Secure storage is unavailable. Retry sign-out.',
        );
      }
      _emit('Restoring secure session...');
      try {
        final saved = await store.read(discovery.server.issuer);
        _check(generation);
        if (saved == null || saved == '{}') return;
        if (saved.length > 16384) throw invalidAuth;
        _tokens = MobileTokens.parse(
          jsonDecode(saved),
          discovery.server,
          now(),
          restoring: true,
        );
        await _me(generation);
      } on RefreshDeferred {
        rethrow;
      } catch (_) {
        await _clear();
        throw const VerificationFailure(
          'Saved session could not be verified. Sign in again.',
        );
      } finally {
        _emit('');
      }
    });
  }

  Future<void> login({required bool rememberMe}) {
    final generation = _generation;
    return _serial(() async {
      _check(generation);
      if (_storageBlocked) {
        throw const VerificationFailure(
          'Secure storage is unavailable. Retry sign-out.',
        );
      }
      if (_tokens != null) {
        throw const VerificationFailure(
          'Sign out before starting another login.',
        );
      }
      // Pending secrets exist only on this stack and are released on every exit.
      final pending = PendingLogin(discovery.server.issuer, now());
      _emit('Opening browser sign-in...');
      try {
        final begin = await transport.requestJson(
          discovery.url('begin'),
          method: 'POST',
          data: {
            'code_challenge': pkceChallenge(pending.verifier),
            'code_challenge_method': 'S256',
            'state': pending.state,
            'remember_me': rememberMe,
            'device_name': 'AT Mobile',
          },
        );
        _check(generation);
        final url = discovery.authorizationUrl(begin);
        final remaining = pending.expires.difference(now());
        if (remaining <= Duration.zero) throw invalidAuth;
        _emit('Complete sign-in and approve in your browser.');
        final callback = await browser.open(url).timeout(remaining);
        _check(generation);
        final code = pending.consume(callback, now());
        _emit('Exchanging authorization code...');
        final response = await transport.requestJson(
          discovery.url('token'),
          method: 'POST',
          data: {'code': code, 'code_verifier': pending.verifier},
        );
        _check(generation);
        final tokens = MobileTokens.parse(response, discovery.server, now());
        if (tokens.rememberMe != rememberMe) throw invalidAuth;
        await _commit(tokens, generation);
        await _me(generation);
      } on VerificationFailure {
        rethrow;
      } catch (_) {
        throw const VerificationFailure(
          'Sign-in did not complete. Start a new browser sign-in.',
        );
      } finally {
        _emit('');
      }
    });
  }

  Future<void> _rotate(int generation) async {
    _check(generation);
    final previous = _tokens;
    if (previous == null || !previous.sessionExpires.isAfter(now())) {
      await _clear();
      throw const VerificationFailure('Session expired. Sign in again.');
    }
    if (_retryAt != null && now().isBefore(_retryAt!)) {
      throw const RefreshDeferred();
    }
    _emit('Refreshing secure session...');
    // Delete before dispatch: a crash/lost response must never replay an old
    // single-use refresh on next launch. In-memory refresh stays single-flight.
    await store.delete(discovery.server.issuer);
    try {
      final response = await transport.requestJson(
        discovery.url('refresh'),
        method: 'POST',
        data: {'refresh_token': previous.refresh},
      );
      _check(generation);
      final replacement = MobileTokens.parse(response, discovery.server, now());
      replacement.validateRotation(previous);
      await _commit(replacement, generation);
      _retryAt = null;
    } on HttpFailure catch (e) {
      if (e.status == 429 && generation == _generation && !_disposed) {
        _retryAt = now().add(
          Duration(
            seconds: e.retryAfter != null && e.retryAfter! > 0
                ? e.retryAfter!
                : 300,
          ),
        );
        await _commit(previous, generation);
        throw const RefreshDeferred();
      }
      await _clear();
      throw const VerificationFailure(
        'Refresh was not confirmed. Sign in again; the old token will not be retried.',
      );
    } catch (_) {
      await _clear();
      throw const VerificationFailure(
        'Refresh was not confirmed. Sign in again; the old token will not be retried.',
      );
    }
  }

  Future<void> refresh() {
    if (_refreshFlight != null) return _refreshFlight!;
    final generation = _generation;
    final flight = _serial(() async {
      try {
        await _rotate(generation);
      } finally {
        _emit('');
      }
    });
    _refreshFlight = flight;
    flight.then(
      (_) {
        _refreshFlight = null;
      },
      onError: (Object _, StackTrace _) {
        _refreshFlight = null;
      },
    );
    return flight;
  }

  Future<Object?> _get(
    String path,
    int generation, {
    Map<String, String>? query,
    int maxBytes = 16384,
    CancelToken? cancel,
  }) async {
    _check(generation);
    if (!RegExp(r'^(auth/me|api/v1/[a-zA-Z0-9/_-]+)$').hasMatch(path)) {
      throw invalidAuth;
    }
    if (_tokens == null) {
      throw const VerificationFailure('Sign in to continue.');
    }
    if (!_tokens!.sessionExpires.isAfter(now())) {
      await _clear();
      throw const VerificationFailure('Session expired. Sign in again.');
    }
    final rotated = !_tokens!.accessExpires.isAfter(now());
    if (rotated) await _rotate(generation);
    final tokens = _tokens!;
    try {
      final response = await transport.requestJson(
        discovery.server.uri.resolve(path).replace(queryParameters: query),
        bearer: tokens.access,
        maxBytes: maxBytes,
        cancel: cancel,
      );
      _check(generation);
      return response;
    } on HttpFailure catch (e) {
      if (e.status != 401) rethrow;
      if (rotated) {
        await _clear();
        rethrow;
      }
      // A resource 401 is not itself proof that the access credential expired.
      if (path != 'auth/me') {
        var accessRejected = false;
        try {
          final me = await transport.requestJson(
            discovery.url('me'),
            bearer: tokens.access,
          );
          _check(generation);
          tokens.validateMe(me);
        } on HttpFailure catch (checkError) {
          if (checkError.status != 401) rethrow;
          accessRejected = true;
        }
        if (!accessRejected) rethrow;
      }
      await _rotate(generation);
      try {
        final response = await transport.requestJson(
          discovery.server.uri.resolve(path).replace(queryParameters: query),
          bearer: _tokens!.access,
          maxBytes: maxBytes,
          cancel: cancel,
        );
        _check(generation);
        return response;
      } on HttpFailure catch (retryError) {
        if (retryError.status == 401) await _clear();
        rethrow;
      }
    }
  }

  Future<void> _me(int generation) async {
    try {
      final value = await _get('auth/me', generation);
      _check(generation);
      account = _tokens!.validateMe(value);
      if (!_disposed) notifyListeners();
    } on HttpFailure catch (e) {
      if (e.status == 401) await _clear();
      rethrow;
    } on RefreshDeferred {
      rethrow;
    } on VerificationFailure {
      if (generation == _generation) await _clear();
      rethrow;
    }
  }

  Future<void> checkAccount() {
    final generation = _generation;
    return _serial(() async {
      _emit('Checking account...');
      try {
        await _me(generation);
      } finally {
        _emit('');
      }
    });
  }

  Future<Object?> authenticatedGet(String path) {
    final generation = _generation;
    return _serial(() => _get(path, generation));
  }

  // MobileSession is the app's auth coordinator. Mutations preflight /me but
  // are never replayed after dispatch; streaming holds no refresh/storage lock.
  Future<String> _chatAccess(int generation, CancelToken cancel) async {
    _check(generation);
    if (cancel.isCancelled) {
      throw const VerificationFailure('Request cancelled.');
    }
    if (!canChat) {
      throw const VerificationFailure(
        'Personal conversations require an administrator account.',
      );
    }
    await _me(generation);
    _check(generation);
    if (_tokens!.accessExpires.isBefore(
      now().add(const Duration(seconds: 30)),
    )) {
      await _rotate(generation);
    }
    _check(generation);
    if (!_tokens!.accessExpires.isAfter(
      now().add(const Duration(seconds: 5)),
    )) {
      throw const VerificationFailure(
        'Session is expiring. Sign in again before sending.',
      );
    }
    if (!canChat || cancel.isCancelled) {
      throw const VerificationFailure('Conversation access is unavailable.');
    }
    _emit('');
    return _tokens!.access;
  }

  Uri _chatUrl(String path, Map<String, String>? query) {
    if (!RegExp(r'^api/v1/conversations(?:/[A-Za-z0-9_-]+)*$').hasMatch(path)) {
      throw invalidAuth;
    }
    return discovery.server.uri.resolve(path).replace(queryParameters: query);
  }

  Future<Object?> authenticatedRequest(
    String path, {
    String method = 'GET',
    Map<String, dynamic>? data,
    Map<String, String>? query,
    int expectedStatus = 200,
    int maxBytes = 262144,
    CancelToken? cancel,
  }) {
    final generation = _generation;
    final token = cancel ?? CancelToken();
    final url = _chatUrl(path, query);
    _activeRequests.add(token);
    return _serial(() async {
      try {
        _check(generation);
        if (!canChat || token.isCancelled) {
          throw const VerificationFailure(
            'Personal conversations require an administrator account.',
          );
        }
        final Object? result;
        if (method == 'GET') {
          result = await _get(
            path,
            generation,
            query: query,
            maxBytes: maxBytes,
            cancel: token,
          );
        } else {
          if (!{'POST', 'PATCH', 'DELETE'}.contains(method)) throw invalidAuth;
          final bearer = await _chatAccess(generation, token);
          result = await transport.requestJson(
            url,
            method: method,
            data: data,
            bearer: bearer,
            expectedStatus: expectedStatus,
            maxBytes: maxBytes,
            cancel: token,
          );
        }
        _check(generation);
        if (token.isCancelled || !canChat) {
          throw const VerificationFailure('Conversation access changed.');
        }
        return result;
      } finally {
        _activeRequests.remove(token);
      }
    });
  }

  Stream<List<int>> authenticatedStream(
    String path,
    Map<String, dynamic> data,
    CancelToken token,
  ) async* {
    final generation = _generation;
    final url = _chatUrl(path, null);
    _activeRequests.add(token);
    try {
      final bearer = await _serial(() => _chatAccess(generation, token));
      await for (final chunk in transport.requestStream(
        url,
        data: data,
        bearer: bearer,
        cancel: token,
      )) {
        _check(generation);
        if (token.isCancelled || !canChat) {
          throw const VerificationFailure('Conversation access changed.');
        }
        yield chunk;
      }
    } finally {
      token.cancel('stream closed');
      _activeRequests.remove(token);
    }
  }

  Future<String> logout() {
    ++_generation; // Immediately invalidate every in-flight result before waiting.
    _cancelRequests();
    final refresh = _tokens?.refresh;
    account = null;
    _emit('Signing out...');
    return _serial(() async {
      var revoked = false;
      try {
        var cleanupFailed = false;
        try {
          await _clear();
          _storageBlocked = false;
        } catch (_) {
          cleanupFailed = true;
        }
        if (refresh != null) {
          try {
            await transport.requestJson(
              discovery.url('logout'),
              method: 'POST',
              data: {'refresh_token': refresh},
              expectedStatus: 204,
            );
            revoked = true;
          } catch (_) {
            /* Local logout is not proof of server revocation. */
          }
        }
        if (cleanupFailed) {
          throw VerificationFailure(
            revoked
                ? 'Server session revoked, but local secure cleanup failed. Unlock the device and retry sign-out.'
                : 'Local secure cleanup and server revocation were not confirmed. Unlock the device and retry sign-out.',
          );
        }
        return revoked
            ? 'Signed out. This mobile session was revoked.'
            : 'Local session removed. Server revocation was not confirmed; it may still be active.';
      } finally {
        _emit('');
      }
    });
  }

  @override
  void dispose() {
    _cancelRequests();
    _disposed = true;
    ++_generation;
    super.dispose();
  }
}
