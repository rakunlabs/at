import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:at_mobile/auth.dart';
import 'package:at_mobile/auth_models.dart';
import 'package:at_mobile/server.dart';
import 'package:flutter_test/flutter_test.dart';

import 'auth_fixtures.dart';

void main() {
  test('S256 RFC7636 vector and independent canonical 32-byte secrets', () {
    expect(
      pkceChallenge('dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk'),
      'E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM',
    );
    final pending = PendingLogin(authServer.issuer, authNow);
    expect(canonicalSecret(pending.verifier), true);
    expect(canonicalSecret(pending.state), true);
    expect(pending.verifier, isNot(pending.state));
    expect(canonicalSecret('${secret(1)}='), false);
  });

  test('discovery pins every endpoint, issuer and version', () {
    MobileAuth.parse(descriptor(), authServer);
    for (final entry in {
      'version': 2,
      'issuer': '${authServer.issuer}/',
      'token_endpoint': 'https://evil.example/token',
      'callback_uri': 'other://auth/callback',
      'code_challenge_methods_supported': ['plain'],
      'request_expires_in': 600,
    }.entries) {
      expect(
        () => MobileAuth.parse({
          ...descriptor(),
          entry.key: entry.value,
        }, authServer),
        throwsA(isA<VerificationFailure>()),
      );
    }
  });

  test('authorization URL accepts only exact canonical approval route', () {
    final discovery = MobileAuth.parse(descriptor(), authServer);
    final good = '${authServer.uri}#/mobile-authorize?request_id=${secret(3)}';
    expect(
      discovery.authorizationUrl({
        'authorization_url': good,
        'expires_in': 300,
      }).toString(),
      good,
    );
    for (final url in [
      good.replaceFirst('example.com', 'evil.example'),
      good.replaceFirst('/at/', '/'),
      good.replaceFirst('/at/', '/other/../at/'),
      good.replaceFirst('#/', '?redirect=evil#/'),
      '$good&redirect=https://evil.example',
      good.replaceFirst('https://', 'https://user@example.net@'),
      good.replaceFirst('mobile-authorize', 'login'),
    ]) {
      expect(
        () => discovery.authorizationUrl({
          'authorization_url': url,
          'expires_in': 300,
        }),
        throwsA(isA<VerificationFailure>()),
      );
    }
  });

  test(
    'callback state, authority, path, duplicates and ambiguity rejected',
    () {
      for (final alter in <String Function(String)>[
        (s) => s.replaceFirst('atmobile:', 'other:'),
        (s) => s.replaceFirst('//auth', '//evil'),
        (s) => s.replaceFirst('//auth', '//user@auth'),
        (s) => s.replaceFirst('//auth', '//@auth'),
        (s) => s.replaceFirst('//auth', '//auth:80'),
        (s) => s.replaceFirst('/callback?', '/x/../callback?'),
        (s) => '$s#fragment',
        (s) => '$s&state=${secret(2)}',
        (s) => '$s&code=${secret(2)}',
        (s) => '$s&error=access_denied',
        (s) => '$s&issuer=https://evil.example',
        (s) => s.replaceFirst(RegExp('state=[^&]+'), 'state=${secret(2)}'),
      ]) {
        final attempt = PendingLogin(authServer.issuer, authNow);
        expect(
          () => attempt.consume(
            alter('$callbackUri?state=${attempt.state}&code=${secret(3)}'),
            authNow,
          ),
          throwsA(isA<VerificationFailure>()),
        );
      }
    },
  );

  test('callback is single-use and bounded; denial still requires state', () {
    final attempt = PendingLogin(authServer.issuer, authNow);
    final callback = '$callbackUri?code=${secret(2)}&state=${attempt.state}';
    expect(attempt.consume(callback, authNow), secret(2));
    expect(
      () => attempt.consume(callback, authNow),
      throwsA(isA<VerificationFailure>()),
    );
    final expired = PendingLogin(authServer.issuer, authNow);
    expect(
      () => expired.consume(
        '$callbackUri?code=${secret(2)}&state=${expired.state}',
        authNow.add(const Duration(minutes: 5)),
      ),
      throwsA(isA<VerificationFailure>()),
    );
    final denied = PendingLogin(authServer.issuer, authNow);
    expect(
      () => denied.consume(
        '$callbackUri?error=access_denied&state=${denied.state}',
        authNow,
      ),
      throwsA(
        isA<VerificationFailure>().having(
          (e) => e.message,
          'denied',
          contains('denied'),
        ),
      ),
    );
  });

  test('token DTO validates issuer, credentials, deadlines and identity', () {
    final good = tokenJson();
    expect(
      MobileTokens.parse(good, authServer, authNow).identity.roles,
      isEmpty,
    );
    for (final entry in <String, Object?>{
      'issuer': '${authServer.issuer}/',
      'token_type': 'bearer',
      'access_token': 'gateway-key',
      'refresh_token': good['access_token'],
      'session_id': 'not-a-family',
      'remember_me': 'false',
      'access_expires_at': authNow
          .subtract(const Duration(seconds: 1))
          .toIso8601String(),
      'session_expires_at': authNow
          .add(const Duration(minutes: 1))
          .toIso8601String(),
      'identity': {'subject': '', 'name': 'x', 'provider': 'local'},
    }.entries) {
      expect(
        () => MobileTokens.parse(
          {...good, entry.key: entry.value},
          authServer,
          authNow,
        ),
        throwsA(isA<VerificationFailure>()),
      );
    }
    expect(
      () => authTime('2026-09-07T12:00:00'),
      throwsA(isA<VerificationFailure>()),
    );
  });

  test(
    'me subject/family/expiry must agree; rotation cannot change family',
    () {
      final tokens = MobileTokens.parse(tokenJson(), authServer, authNow);
      tokens.validateMe(meJson(tokenJson()));
      for (final changed in [
        {...meJson(tokenJson()), 'subject': 'another'},
        {
          ...meJson(tokenJson()),
          'claims': {
            ...meJson(tokenJson())['claims'] as Map,
            'session_id': 'b' * 64,
          },
        },
        {...meJson(tokenJson()), 'expires_at': authNow.toIso8601String()},
      ]) {
        expect(
          () => tokens.validateMe(changed),
          throwsA(isA<VerificationFailure>()),
        );
      }
      expect(
        () => MobileTokens.parse(
          {...tokenJson(version: 2), 'session_id': 'b' * 64},
          authServer,
          authNow,
        ).validateRotation(tokens),
        throwsA(isA<VerificationFailure>()),
      );
    },
  );

  group('session lifecycle', () {
    late FakeVault vault;
    late FakeAuthTransport transport;
    late FakeBrowser browser;
    late MobileSession session;
    late Map<String, dynamic> current;
    late DateTime clock;
    setUp(() {
      vault = FakeVault();
      transport = FakeAuthTransport();
      browser = FakeBrowser();
      current = tokenJson();
      clock = authNow;
      session = MobileSession(
        MobileAuth.parse(descriptor(), authServer),
        transport: transport,
        store: vault,
        browser: browser,
        clock: () => clock,
      );
      transport.handler = (req) async {
        if (req.url.path.endsWith('/me')) return meJson(current);
        if (req.url.path.endsWith('/begin')) {
          return {
            'authorization_url':
                '${authServer.uri}#/mobile-authorize?request_id=${secret(4)}',
            'expires_in': 300,
          };
        }
        if (req.url.path.endsWith('/refresh')) {
          current = tokenJson(version: 2);
          return current;
        }
        if (req.url.path.endsWith('/logout')) return null;
        return current;
      };
      browser.handler = (_) async =>
          '$callbackUri?code=${secret(5)}&state=${transport.calls.first.data!['state']}';
    });
    tearDown(() {
      session.dispose();
      transport.close();
    });
    Future<void> restore() async {
      vault.values[authServer.issuer] = jsonEncode(current);
      await session.restore();
    }

    test(
      'login uses PKCE, persists one pair, and verifies non-admin me',
      () async {
        await session.login(rememberMe: false);
        expect(session.account!.roles, isEmpty);
        expect(session.account!.subject, '01USER');
        expect(vault.values.length, 1);
        final begin = transport.calls.first;
        final exchange = transport.calls[1];
        expect(begin.bearer, isNull);
        expect(exchange.bearer, isNull);
        expect(
          begin.data!['code_challenge'],
          pkceChallenge(exchange.data!['code_verifier'] as String),
        );
        expect(transport.calls.last.bearer, current['access_token']);
        expect(
          jsonDecode(vault.values.values.single)['access_token'],
          current['access_token'],
        );
      },
    );

    test('wrong callback state never exchanges', () async {
      browser.handler = (_) async =>
          '$callbackUri?code=${secret(5)}&state=${secret(6)}';
      await expectLater(
        session.login(rememberMe: false),
        throwsA(isA<VerificationFailure>()),
      );
      expect(transport.calls.length, 1);
      expect(vault.values, isEmpty);
    });

    test('restore calls bearer me before exposing identity', () async {
      await restore();
      expect(transport.calls.single.url.path, '/at/auth/me');
      expect(transport.calls.single.bearer, current['access_token']);
      expect(session.account!.name, 'reader@example.com');
    });

    test('expired absolute session clears without network', () async {
      vault.values[authServer.issuer] = jsonEncode({
        ...current,
        'session_expires_at': authNow.toIso8601String(),
      });
      await expectLater(session.restore(), throwsA(isA<VerificationFailure>()));
      expect(transport.calls, isEmpty);
      expect(vault.values, isEmpty);
    });

    test('refresh single-flight persists replacement without replay', () async {
      await restore();
      final gate = Completer<Object?>();
      transport.handler = (_) => gate.future;
      final first = session.refresh();
      final second = session.refresh();
      expect(identical(first, second), true);
      await Future<void>.delayed(Duration.zero);
      expect(vault.values, isEmpty);
      gate.complete(tokenJson(version: 2));
      await Future.wait([first, second]);
      expect(
        transport.calls.where((r) => r.url.path.endsWith('/refresh')).length,
        1,
      );
      expect(
        jsonDecode(vault.values.values.single)['refresh_token'],
        secret(12),
      );
    });

    test('concurrent authenticated GETs rotate once after 401', () async {
      await restore();
      transport.handler = (request) async {
        if (request.url.path.endsWith('/refresh')) return tokenJson(version: 2);
        if (request.bearer == secret(1)) throw const HttpFailure(401, null);
        expect(request.bearer, secret(2));
        return {'ok': true};
      };
      final results = await Future.wait([
        session.authenticatedGet('api/v1/info'),
        session.authenticatedGet('api/v1/info'),
      ]);
      expect(results, [
        {'ok': true},
        {'ok': true},
      ]);
      expect(
        transport.calls.where((r) => r.url.path.endsWith('/refresh')).length,
        1,
      );
    });

    test('expired access followed by 401 does not rotate twice', () async {
      await restore();
      clock = authNow.add(const Duration(minutes: 11));
      transport.handler = (request) async {
        if (request.url.path.endsWith('/refresh')) {
          return {
            ...tokenJson(version: 2),
            'access_expires_at': clock
                .add(const Duration(minutes: 10))
                .toIso8601String(),
          };
        }
        throw const HttpFailure(401, null);
      };
      await expectLater(
        session.checkAccount(),
        throwsA(isA<VerificationFailure>()),
      );
      expect(
        transport.calls.where((r) => r.url.path.endsWith('/refresh')).length,
        1,
      );
      expect(vault.values, isEmpty);
      expect(session.account, isNull);
    });

    test(
      'logout still revokes if local cleanup fails and blocks new login',
      () async {
        await restore();
        vault.failDelete = true;
        vault.failWrite = true;
        await expectLater(
          session.logout(),
          throwsA(
            isA<VerificationFailure>().having(
              (e) => e.message,
              'message',
              contains('local secure cleanup failed'),
            ),
          ),
        );
        expect(transport.calls.last.url.path, '/at/auth/mobile/logout');
        expect(transport.calls.last.data, {'refresh_token': secret(11)});
        await expectLater(
          session.login(rememberMe: false),
          throwsA(isA<VerificationFailure>()),
        );
        expect(browser.calls, 0);
        expect(session.account, isNull);
      },
    );

    test('ambiguous timeout clears old refresh and never retries it', () async {
      await restore();
      transport.handler = (_) async =>
          throw TimeoutException('sensitive request data');
      await expectLater(session.refresh(), throwsA(isA<VerificationFailure>()));
      await expectLater(session.refresh(), throwsA(isA<VerificationFailure>()));
      expect(
        transport.calls.where((r) => r.url.path.endsWith('/refresh')).length,
        1,
      );
      expect(vault.values, isEmpty);
      expect(session.account, isNull);
    });

    test('429 restores unconsumed token and respects Retry-After', () async {
      await restore();
      transport.handler = (_) async => throw const HttpFailure(429, 60);
      await expectLater(session.refresh(), throwsA(isA<RefreshDeferred>()));
      await expectLater(session.refresh(), throwsA(isA<RefreshDeferred>()));
      expect(transport.calls.length, 2);
      expect(
        jsonDecode(vault.values.values.single)['refresh_token'],
        secret(11),
      );
    });

    test('failed rotation persistence removes stale credentials', () async {
      await restore();
      vault.failWrite = true;
      await expectLater(session.refresh(), throwsA(isA<VerificationFailure>()));
      expect(vault.values, isEmpty);
      expect(session.metadata, isNull);
    });

    test('logout during refresh cannot resurrect session', () async {
      await restore();
      final gate = Completer<Object?>();
      transport.handler = (req) async =>
          req.url.path.endsWith('/refresh') ? gate.future : null;
      final rotating = session.refresh();
      final failed = expectLater(rotating, throwsA(isA<VerificationFailure>()));
      await Future<void>.delayed(Duration.zero);
      final loggingOut = session.logout();
      expect(session.account, isNull);
      gate.complete(tokenJson(version: 2));
      await failed;
      expect(await loggingOut, contains('revoked'));
      expect(vault.values, isEmpty);
      expect(session.metadata, isNull);
      expect(transport.calls.last.data, {'refresh_token': secret(11)});
      expect(transport.calls.last.bearer, isNull);
    });

    test('logout during delayed secure commit cleans after write', () async {
      await restore();
      vault.writeGate = Completer<void>();
      final rotating = session.refresh();
      final failed = expectLater(rotating, throwsA(isA<VerificationFailure>()));
      await Future<void>.delayed(Duration.zero);
      final loggingOut = session.logout();
      vault.writeGate!.complete();
      await failed;
      await loggingOut;
      expect(vault.values, isEmpty);
      expect(session.account, isNull);
    });

    test('logout uses refresh without live access and reports uncertain revocation', () async {
      await restore();
      clock = authNow.add(const Duration(minutes: 11));
      transport.handler = (_) async => throw const HttpFailure(503, null);
      final message = await session.logout();
      expect(message, contains('may still be active'));
      expect(transport.calls.last.data!['refresh_token'], secret(11));
      expect(transport.calls.last.bearer, isNull);
      expect(vault.values, isEmpty);
    });

    test(
      'different server namespace never reads or sends first server tokens',
      () async {
        await restore();
        final other = ServerAddress.parse('https://example.com/other/');
        final second = MobileSession(
          MobileAuth.parse(descriptor(other), other),
          transport: transport,
          store: vault,
          browser: browser,
          clock: () => authNow,
        );
        await second.restore();
        expect(second.account, isNull);
        expect(transport.calls.length, 1);
        expect(vault.values.containsKey(authServer.issuer), true);
        second.dispose();
      },
    );

    test(
      'authenticated GET never permits absolute or gateway routes',
      () async {
        await restore();
        for (final path in [
          'https://evil.example',
          '/auth/me',
          'gateway/v1/models',
          'api/v1/../auth',
        ]) {
          await expectLater(
            session.authenticatedGet(path),
            throwsA(isA<VerificationFailure>()),
          );
        }
        expect(transport.calls.length, 1);
      },
    );
  });

  test(
    'native bearer transport never follows redirects or sends cookies',
    () async {
      final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      final transport = StatusClient();
      server.listen((req) async {
        expect(req.headers.value('cookie'), isNull);
        expect(req.headers.value('authorization'), 'Bearer ${secret(1)}');
        req.response.statusCode = 302;
        req.response.headers.set('location', 'https://evil.example/auth/me');
        await req.response.close();
      });
      try {
        await expectLater(
          transport.requestJson(
            Uri.parse('http://127.0.0.1:${server.port}/at/auth/me'),
            bearer: secret(1),
          ),
          throwsA(isA<VerificationFailure>()),
        );
      } finally {
        transport.close();
        await server.close(force: true);
      }
    },
  );
}
