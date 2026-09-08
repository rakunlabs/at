import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:dio/dio.dart';
import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';

class ServerAddress {
  const ServerAddress._(this.uri);
  final Uri uri;

  factory ServerAddress.parse(String input, {bool allowLocalHttp = false}) {
    final text = input.trim();
    final raw = RegExp(r'^[a-zA-Z]+://([^/?#]*)([^?#]*)').firstMatch(text);
    if (raw == null ||
        raw.group(1)!.contains('@') ||
        raw.group(2)!.split('/').any((segment) {
          final decoded = segment.replaceAll(
            RegExp('%2e', caseSensitive: false),
            '.',
          );
          return decoded == '.' || decoded == '..';
        })) {
      throw const FormatException(
        'Enter a server URL without credentials or path traversal.',
      );
    }
    final uri = Uri.tryParse(text);
    if (uri == null ||
        !uri.hasAuthority ||
        uri.host.isEmpty ||
        uri.userInfo.isNotEmpty ||
        uri.authority.contains('@') ||
        uri.hasQuery ||
        uri.hasFragment ||
        RegExp(r'[\s\\\x00-\x1f\x7f]').hasMatch(text)) {
      throw const FormatException(
        'Enter a server URL without credentials, query, or fragment.',
      );
    }
    final loopback = {'localhost', '127.0.0.1', '::1'}.contains(uri.host);
    if (uri.scheme != 'https' &&
        !(allowLocalHttp && loopback && uri.scheme == 'http')) {
      throw const FormatException(
        'Use HTTPS, for example https://at.example.com/at/.',
      );
    }
    if (uri.port < 1 ||
        uri.port > 65535 ||
        (!uri.host.contains(':') &&
            !RegExp(r'^[a-z0-9.-]+$').hasMatch(uri.host))) {
      throw const FormatException('Enter a valid server host and port.');
    }
    // Reject ambiguous paths rather than let proxies decode a different base path.
    if (uri.pathSegments.any(
      (s) =>
          s == '.' || s == '..' || RegExp(r'[%/\\\x00-\x20\x7f]').hasMatch(s),
    )) {
      throw const FormatException(
        'Use a base path without encoded separators or traversal.',
      );
    }
    final path = uri.path.replaceFirst(RegExp(r'/+$'), '');
    return ServerAddress._(uri.replace(path: '$path/'));
  }

  Uri get statusUrl => uri.resolve('auth/status');
  String get issuer => toString().substring(0, toString().length - 1);
  Uri get webLoginUrl => uri.resolve('#/');
  @override
  String toString() => uri.toString();
}

class AuthStatus {
  const AuthStatus({
    required this.enabled,
    required this.passkeys,
    required this.rememberMe,
    required this.passkeyLogin,
    this.mobileAuth,
  });
  final bool enabled;
  final bool passkeys;
  final bool rememberMe;
  final String passkeyLogin;
  final Object? mobileAuth;

  factory AuthStatus.fromJson(Object? value) {
    if (value is! Map<String, dynamic> ||
        value['enabled'] is! bool ||
        value['passkeys'] is! bool ||
        value['remember_me'] is! bool ||
        value['passkey_login'] != 'username-first') {
      throw const VerificationFailure(
        'This server returned an unsupported auth status. Check the URL and AT version.',
      );
    }
    return AuthStatus(
      enabled: value['enabled'] as bool,
      passkeys: value['passkeys'] as bool,
      rememberMe: value['remember_me'] as bool,
      passkeyLogin: value['passkey_login'] as String,
      mobileAuth: value['mobile_auth'],
    );
  }
}

class VerificationFailure implements Exception {
  const VerificationFailure(this.message);
  final String message;
  @override
  String toString() => message;
}

class HttpFailure extends VerificationFailure {
  const HttpFailure(this.status, this.retryAfter)
    : super(
        'The requested authentication operation is unavailable. Try again or sign in.',
      );
  final int? status;
  final int? retryAfter;
}

// CancelToken futures cannot unsubscribe. One weakly keyed guard per parent
// retains only active children, never a callback/payload for every past request.
class _ParentCancellation {
  final children = <CancelToken>{};
  void cancel() {
    for (final child in children.toList()) {
      child.cancel('parent cancelled');
    }
    children.clear();
  }
}

class _RequestCancellation {
  _RequestCancellation(CancelToken? parent) {
    if (parent == null) return;
    if (parent.isCancelled) {
      token.cancel('parent cancelled');
      return;
    }
    var guard = _parents[parent];
    if (guard == null) {
      guard = _ParentCancellation();
      _parents[parent] = guard;
      final linked = guard;
      parent.whenCancel.then((_) => linked.cancel());
    }
    _guard = guard;
    guard.children.add(token);
  }
  static final _parents = Expando<_ParentCancellation>();
  final token = CancelToken();
  _ParentCancellation? _guard;
  void close() {
    _guard?.children.remove(token);
    _guard = null;
    token.cancel('request ended');
  }
}

class StatusClient {
  StatusClient({Dio? dio, this.deadline = const Duration(seconds: 15)})
    : _dio = dio ?? Dio();
  final Dio _dio;
  final Duration deadline;

  Future<AuthStatus> verify(ServerAddress server, CancelToken cancel) async =>
      AuthStatus.fromJson(await requestJson(server.statusUrl, cancel: cancel));

  Future<Object?> requestJson(
    Uri url, {
    CancelToken? cancel,
    String method = 'GET',
    Map<String, dynamic>? data,
    String? bearer,
    int expectedStatus = 200,
    int maxBytes = 16384,
  }) async {
    final cancellation = _RequestCancellation(cancel);
    final token = cancellation.token;
    final timer = Timer(deadline, () => token.cancel('deadline'));
    try {
      final response = await _dio.requestUri<ResponseBody>(
        url,
        data: data,
        cancelToken: token,
        options: Options(
          method: method,
          followRedirects: false,
          maxRedirects: 0,
          responseType: ResponseType.stream,
          receiveTimeout: deadline,
          sendTimeout: deadline,
          headers: {
            'Accept': 'application/json',
            if (data != null) 'Content-Type': 'application/json',
            if (bearer != null) 'Authorization': 'Bearer $bearer',
          },
          validateStatus: (_) => true,
        ),
      );
      if (response.statusCode != null &&
          response.statusCode! >= 300 &&
          response.statusCode! < 400) {
        throw const VerificationFailure(
          'The server redirected the request. Enter its final HTTPS address; redirects are not followed.',
        );
      }
      if (response.statusCode != expectedStatus) {
        throw HttpFailure(
          response.statusCode,
          int.tryParse(response.headers.value('retry-after') ?? ''),
        );
      }
      if (expectedStatus == 204) {
        await response.data?.stream.drain<void>();
        return null;
      }
      final body = response.data;
      if (body == null) {
        throw const VerificationFailure(
          'This server returned an invalid auth status.',
        );
      }
      final bytes = BytesBuilder(copy: false);
      await for (final chunk in body.stream) {
        if (chunk.length > maxBytes - bytes.length) {
          throw const VerificationFailure(
            'This server returned an oversized auth status.',
          );
        }
        bytes.add(chunk);
      }
      return jsonDecode(utf8.decode(bytes.takeBytes()));
    } on VerificationFailure {
      token.cancel('rejected response');
      rethrow;
    } on FormatException {
      throw const VerificationFailure(
        'The response was not valid JSON. Use the AT base URL, not a gateway endpoint.',
      );
    } on DioException catch (e) {
      if (CancelToken.isCancel(e)) {
        throw VerificationFailure(
          token.cancelError?.error == 'deadline'
              ? 'Verification timed out. Check your connection and try again.'
              : 'Verification cancelled.',
        );
      }
      throw const VerificationFailure(
        'Could not securely reach this server. Check your network, URL, and TLS certificate, then try again.',
      );
    } finally {
      timer.cancel();
      cancellation.close();
    }
  }

  void close() => _dio.close(force: true);

  Stream<List<int>> requestStream(
    Uri url, {
    required Map<String, dynamic> data,
    required String bearer,
    required CancelToken cancel,
  }) async* {
    final cancellation = _RequestCancellation(cancel);
    final token = cancellation.token;
    final timer = Timer(
      const Duration(minutes: 6),
      () => token.cancel('stream deadline'),
    );
    try {
      final response = await _dio.requestUri<ResponseBody>(
        url,
        data: data,
        cancelToken: token,
        options: Options(
          method: 'POST',
          followRedirects: false,
          maxRedirects: 0,
          responseType: ResponseType.stream,
          sendTimeout: deadline,
          receiveTimeout: const Duration(seconds: 30),
          validateStatus: (_) => true,
          headers: {
            'Accept': 'text/event-stream',
            'Content-Type': 'application/json',
            'Authorization': 'Bearer $bearer',
          },
        ),
      );
      if (response.statusCode != 200) {
        throw HttpFailure(response.statusCode, null);
      }
      if (response.headers
                  .value('content-type')
                  ?.split(';')
                  .first
                  .trim()
                  .toLowerCase() !=
              'text/event-stream' ||
          response.data == null) {
        throw const VerificationFailure(
          'The server did not return a conversation stream.',
        );
      }
      yield* response.data!.stream;
    } on HttpFailure {
      rethrow;
    } catch (_) {
      throw const VerificationFailure(
        'The reply stream was interrupted. Recover its saved status.',
      );
    } finally {
      timer.cancel();
      cancellation.close();
    }
  }
}

class ServerPreferences {
  late final SharedPreferencesAsync _prefs = SharedPreferencesAsync();
  Future<String?> load() => _prefs.getString('at.selected_server');
  Future<void> save(ServerAddress server) =>
      _prefs.setString('at.selected_server', server.toString());
  Future<void> clear() => _prefs.remove('at.selected_server');
}

// Never available in release, even if a release build supplies the define.
const allowDevelopmentHttp =
    !kReleaseMode && bool.fromEnvironment('AT_ALLOW_LOCAL_HTTP');
