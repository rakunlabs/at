import 'dart:convert';
import 'dart:math';

import 'package:crypto/crypto.dart';

import 'server.dart';

const callbackUri = 'atmobile://auth/callback';
const invalidAuth = VerificationFailure(
  'Invalid authentication response. Start sign-in again.',
);

String randomSecret() {
  final random = Random.secure();
  return base64Url
      .encode(List<int>.generate(32, (_) => random.nextInt(256)))
      .replaceAll('=', '');
}

String pkceChallenge(String verifier) => base64Url
    .encode(sha256.convert(ascii.encode(verifier)).bytes)
    .replaceAll('=', '');

bool canonicalSecret(Object? value) {
  if (value is! String || !RegExp(r'^[A-Za-z0-9_-]{43}$').hasMatch(value)) {
    return false;
  }
  return base64Url.encode(base64Url.decode('$value=')).replaceAll('=', '') ==
      value;
}

Map<String, dynamic> authObject(Object? value) {
  if (value is! Map<String, dynamic>) throw invalidAuth;
  return value;
}

DateTime authTime(Object? value) {
  if (value is! String ||
      !RegExp(
        r'^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$',
      ).hasMatch(value)) {
    throw invalidAuth;
  }
  final time = DateTime.tryParse(value);
  if (time == null) throw invalidAuth;
  if (!value.endsWith('Z')) {
    final zone = value.substring(value.length - 6);
    if (int.parse(zone.substring(1, 3)) > 23 ||
        int.parse(zone.substring(4)) > 59) {
      throw invalidAuth;
    }
  }
  final year = int.parse(value.substring(0, 4));
  final month = int.parse(value.substring(5, 7));
  final day = int.parse(value.substring(8, 10));
  if (month < 1 ||
      month > 12 ||
      day < 1 ||
      day > DateTime.utc(year, month + 1, 0).day ||
      int.parse(value.substring(11, 13)) > 23 ||
      int.parse(value.substring(14, 16)) > 59 ||
      int.parse(value.substring(17, 19)) > 59) {
    throw invalidAuth;
  }
  return time.toUtc();
}

class MobileAuth {
  MobileAuth.parse(Object? value, this.server) {
    final map = authObject(value);
    if (map['version'] is! int ||
        map['version'] != 1 ||
        map['issuer'] != server.issuer ||
        map['callback_uri'] != callbackUri ||
        map['request_expires_in'] != 300 ||
        map['code_expires_in'] != 60 ||
        jsonEncode(map['code_challenge_methods_supported']) != '["S256"]') {
      throw invalidAuth;
    }
    for (final endpoint in ['begin', 'token', 'refresh', 'logout', 'me']) {
      if (map['${endpoint}_endpoint'] != url(endpoint).toString()) {
        throw invalidAuth;
      }
    }
  }
  final ServerAddress server;
  Uri url(String operation) => server.uri.resolve(
    operation == 'me' ? 'auth/me' : 'auth/mobile/$operation',
  );

  Uri authorizationUrl(Object? response) {
    final map = authObject(response);
    if (map['expires_in'] != 300 || map['authorization_url'] is! String) {
      throw invalidAuth;
    }
    final text = map['authorization_url'] as String;
    final prefix = '${server.uri}#/mobile-authorize?request_id=';
    if (!text.startsWith(prefix) ||
        !canonicalSecret(text.substring(prefix.length))) {
      throw invalidAuth;
    }
    return Uri.parse(text);
  }
}

class PendingLogin {
  PendingLogin(this.issuer, DateTime now)
    : verifier = randomSecret(),
      state = randomSecret(),
      expires = now.add(const Duration(minutes: 5));
  final String issuer;
  final String verifier;
  final String state;
  final DateTime expires;
  bool _used = false;

  String consume(String callback, DateTime now) {
    if (_used || !now.isBefore(expires)) throw invalidAuth;
    _used = true;
    // Raw prefix validation also rejects normalized paths, empty userinfo and ports.
    if (!callback.startsWith('$callbackUri?')) throw invalidAuth;
    final uri = Uri.tryParse(callback);
    if (uri == null ||
        uri.hasFragment ||
        uri.hasPort ||
        uri.userInfo.isNotEmpty ||
        uri.scheme != 'atmobile' ||
        uri.host != 'auth' ||
        uri.path != '/callback') {
      throw invalidAuth;
    }
    final params = uri.queryParametersAll;
    if (params.values.any((v) => v.length != 1) ||
        params.keys.any((k) => !{'state', 'code', 'error'}.contains(k)) ||
        params.length != 2 ||
        !canonicalSecret(params['state']?.single)) {
      throw invalidAuth;
    }
    final received = params['state']!.single;
    var difference = 0;
    for (var i = 0; i < state.length; i++) {
      difference |= state.codeUnitAt(i) ^ received.codeUnitAt(i);
    }
    if (difference != 0) throw invalidAuth;
    if (params['error']?.single == 'access_denied') {
      throw const VerificationFailure(
        'Sign-in was denied. No mobile session was created.',
      );
    }
    final code = params['code']?.single;
    if (!canonicalSecret(code)) throw invalidAuth;
    return code!;
  }
}

class AccountIdentity {
  AccountIdentity.parse(Object? value) {
    final map = authObject(value);
    if (map['subject'] is! String ||
        (map['subject'] as String).isEmpty ||
        map['name'] is! String ||
        (map['name'] as String).isEmpty ||
        map['provider'] != 'local' ||
        (map.containsKey('roles') &&
            (map['roles'] is! List ||
                (map['roles'] as List).any((r) => r is! String)))) {
      throw invalidAuth;
    }
    subject = map['subject'] as String;
    name = map['name'] as String;
    roles = List<String>.from(map['roles'] as List? ?? []);
  }
  late final String subject;
  late final String name;
  late final List<String> roles;
}

class MobileTokens {
  MobileTokens.parse(
    Object? value,
    ServerAddress server,
    DateTime now, {
    bool restoring = false,
  }) {
    final map = authObject(value);
    if (map['token_type'] != 'Bearer' ||
        map['issuer'] != server.issuer ||
        !canonicalSecret(map['access_token']) ||
        !canonicalSecret(map['refresh_token']) ||
        map['access_token'] == map['refresh_token'] ||
        map['remember_me'] is! bool ||
        map['session_id'] is! String ||
        !RegExp(r'^[a-f0-9]{64}$').hasMatch(map['session_id'] as String)) {
      throw invalidAuth;
    }
    accessExpires = authTime(map['access_expires_at']);
    sessionExpires = authTime(map['session_expires_at']);
    if (!sessionExpires.isAfter(now) ||
        accessExpires.isAfter(sessionExpires) ||
        (!restoring && !accessExpires.isAfter(now)) ||
        accessExpires.isAfter(now.add(const Duration(minutes: 11))) ||
        sessionExpires.isAfter(
          now.add(
            Duration(days: map['remember_me'] == true ? 30 : 1, minutes: 1),
          ),
        )) {
      throw invalidAuth;
    }
    identity = AccountIdentity.parse(map['identity']);
    final identityMap = authObject(map['identity']);
    if (identityMap.containsKey('claims') ||
        identityMap.containsKey('expires_at')) {
      throw invalidAuth;
    }
    access = map['access_token'] as String;
    refresh = map['refresh_token'] as String;
    sessionId = map['session_id'] as String;
    rememberMe = map['remember_me'] as bool;
    json = Map<String, dynamic>.from(map);
  }
  late final Map<String, dynamic> json;
  late final String access, refresh, sessionId;
  late final bool rememberMe;
  late final DateTime accessExpires, sessionExpires;
  late final AccountIdentity identity;

  void validateRotation(MobileTokens previous) {
    if (sessionId != previous.sessionId ||
        identity.subject != previous.identity.subject ||
        sessionExpires != previous.sessionExpires ||
        rememberMe != previous.rememberMe ||
        access == previous.access ||
        refresh == previous.refresh) {
      throw invalidAuth;
    }
  }

  AccountIdentity validateMe(Object? value) {
    final map = authObject(value);
    final user = AccountIdentity.parse(map);
    final claims = authObject(map['claims']);
    if (user.subject != identity.subject ||
        claims['session_id'] != sessionId ||
        claims['remember_me'] != rememberMe ||
        authTime(map['expires_at']) != accessExpires ||
        authTime(claims['session_expires_at']) != sessionExpires) {
      throw invalidAuth;
    }
    return user;
  }
}
