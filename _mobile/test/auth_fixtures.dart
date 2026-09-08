import 'dart:async';
import 'dart:convert';

import 'package:at_mobile/auth.dart';
import 'package:at_mobile/auth_models.dart';
import 'package:at_mobile/server.dart';
import 'package:dio/dio.dart';

final authNow = DateTime.utc(2026, 9, 7, 12);
final authServer = ServerAddress.parse('https://example.com/at/');
String secret(int n) =>
    base64Url.encode(List.filled(32, n)).replaceAll('=', '');
Map<String, dynamic> descriptor([ServerAddress? server]) {
  final issuer = (server ?? authServer).issuer;
  return {
    'version': 1,
    'issuer': issuer,
    'callback_uri': callbackUri,
    'code_challenge_methods_supported': ['S256'],
    'request_expires_in': 300,
    'code_expires_in': 60,
    for (final op in ['begin', 'token', 'refresh', 'logout'])
      '${op}_endpoint': '$issuer/auth/mobile/$op',
    'me_endpoint': '$issuer/auth/me',
  };
}

Map<String, dynamic> tokenJson({int version = 1, ServerAddress? server}) => {
  'token_type': 'Bearer',
  'access_token': secret(version),
  'refresh_token': secret(version + 10),
  'issuer': (server ?? authServer).issuer,
  'session_id': 'a' * 64,
  'remember_me': false,
  'access_expires_at': authNow
      .add(const Duration(minutes: 10))
      .toIso8601String(),
  'session_expires_at': authNow.add(const Duration(hours: 8)).toIso8601String(),
  'identity': {
    'subject': '01USER',
    'name': 'reader@example.com',
    'provider': 'local',
  },
};
Map<String, dynamic> meJson(Map<String, dynamic> tokens) => {
  ...tokens['identity'] as Map<String, dynamic>,
  'expires_at': tokens['access_expires_at'],
  'claims': {
    'session_id': tokens['session_id'],
    'session_expires_at': tokens['session_expires_at'],
    'remember_me': tokens['remember_me'],
  },
};

class FakeVault implements CredentialStore {
  final values = <String, String>{};
  Completer<void>? writeGate;
  bool failWrite = false;
  bool failDelete = false;
  @override
  Future<String?> read(String issuer) async => values[issuer];
  @override
  Future<void> write(String issuer, String value) async {
    await writeGate?.future;
    if (failWrite) throw StateError('private storage failure');
    values[issuer] = value;
  }

  @override
  Future<void> delete(String issuer) async {
    if (failDelete) throw StateError('private storage failure');
    values.remove(issuer);
  }
}

class FakeBrowser implements LoginBrowser {
  Future<String> Function(Uri)? handler;
  int calls = 0;
  @override
  Future<String> open(Uri url) async {
    calls++;
    return handler!(url);
  }
}

class AuthRequest {
  AuthRequest(
    this.url,
    this.method,
    this.data,
    this.bearer,
    this.expectedStatus,
  );
  final Uri url;
  final String method;
  final Map<String, dynamic>? data;
  final String? bearer;
  final int expectedStatus;
}

class FakeAuthTransport extends StatusClient {
  final calls = <AuthRequest>[];
  Future<Object?> Function(AuthRequest)? handler;
  @override
  Future<Object?> requestJson(
    Uri url, {
    CancelToken? cancel,
    String method = 'GET',
    Map<String, dynamic>? data,
    String? bearer,
    int expectedStatus = 200,
    int maxBytes = 16384,
  }) async {
    final request = AuthRequest(url, method, data, bearer, expectedStatus);
    calls.add(request);
    return handler!(request);
  }
}
