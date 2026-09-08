import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:at_mobile/server.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

const statusJson =
    '{"enabled":true,"passkeys":false,"remember_me":true,"passkey_login":"username-first"}';

void main() {
  test('canonical origin and base path retain endpoint and web URL scope', () {
    for (final input in [
      ' HTTPS://EXAMPLE.COM:443/at ',
      'https://example.com/at/',
    ]) {
      final server = ServerAddress.parse(input);
      expect(server.toString(), 'https://example.com/at/');
      expect(server.statusUrl.toString(), 'https://example.com/at/auth/status');
      expect(server.webLoginUrl.toString(), 'https://example.com/at/#/');
    }
    expect(
      ServerAddress.parse('https://example.com').statusUrl.toString(),
      'https://example.com/auth/status',
    );
    expect(
      ServerAddress.parse('https://example.com/team/at///').statusUrl.path,
      '/team/at/auth/status',
    );
  });

  test('reject unsafe server input', () {
    for (final input in [
      '',
      'example.com',
      'http://example.com',
      'http://localhost',
      'https://user:secret@example.com',
      'https://@example.com',
      'https://example.com?',
      'https://example.com#',
      'https://exa mple.com',
      'https://example.com:99999',
      'https://example.com/a%2fb',
      'https://example.com/a%5cb',
      'https://example.com/%252f',
      'https://example.com/at/../other',
      'https://example.com/%2e%2e/other',
    ]) {
      expect(
        () => ServerAddress.parse(input),
        throwsFormatException,
        reason: input,
      );
    }
  });

  test('development HTTP exception only for explicit loopback hosts', () {
    for (final host in ['localhost', '127.0.0.1', '[::1]']) {
      expect(
        ServerAddress.parse(
          'http://$host:8080/at',
          allowLocalHttp: true,
        ).uri.scheme,
        'http',
      );
    }
    for (final host in ['10.0.2.2', '192.168.1.1', 'localhost.example.com']) {
      expect(
        () => ServerAddress.parse('http://$host', allowLocalHttp: true),
        throwsFormatException,
      );
    }
  });

  test(
    'auth status requires all exact field types and supported login mode',
    () {
      final valid = <String, dynamic>{
        'enabled': false,
        'passkeys': false,
        'remember_me': false,
        'passkey_login': 'username-first',
      };
      expect(AuthStatus.fromJson(valid).enabled, false);
      for (final field in valid.keys) {
        expect(
          () => AuthStatus.fromJson({...valid}..remove(field)),
          throwsA(isA<VerificationFailure>()),
        );
        expect(
          () => AuthStatus.fromJson({...valid, field: 1}),
          throwsA(isA<VerificationFailure>()),
        );
      }
      for (final value in [
        null,
        [],
        'login',
        {...valid, 'passkey_login': 'discoverable'},
      ]) {
        expect(
          () => AuthStatus.fromJson(value),
          throwsA(isA<VerificationFailure>()),
        );
      }
    },
  );

  group('real HTTP transport', () {
    late HttpServer server;
    late StatusClient client;
    late ServerAddress address;
    setUp(() async {
      server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
      address = ServerAddress.parse(
        'http://127.0.0.1:${server.port}/at/',
        allowLocalHttp: true,
      );
      client = StatusClient();
    });
    tearDown(() async {
      client.close();
      await server.close(force: true);
    });

    test('GET preserves base path and sends no credentials', () async {
      server.listen((request) async {
        expect(request.method, 'GET');
        expect(request.uri.path, '/at/auth/status');
        expect(request.headers.value('authorization'), isNull);
        expect(request.headers.value('cookie'), isNull);
        request.response.write(statusJson);
        await request.response.close();
      });
      expect((await client.verify(address, CancelToken())).enabled, true);
    });

    test('cross-host redirect is rejected without following', () async {
      var calls = 0;
      server.listen((request) async {
        calls++;
        request.response.statusCode = 302;
        request.response.headers.set(
          'location',
          'https://example.com/auth/status',
        );
        await request.response.close();
      });
      await expectLater(
        client.verify(address, CancelToken()),
        throwsA(
          isA<VerificationFailure>().having(
            (e) => e.message,
            'message',
            contains('redirected'),
          ),
        ),
      );
      expect(calls, 1);
    });

    test('malformed JSON and HTTP errors are safe', () async {
      server.listen((request) async {
        request.response.statusCode = 403;
        request.response.write('private upstream details');
        await request.response.close();
      });
      await expectLater(
        client.verify(address, CancelToken()),
        throwsA(
          isA<VerificationFailure>().having(
            (e) => e.message,
            'safe message',
            isNot(contains('private')),
          ),
        ),
      );
    });

    test('invalid JSON rejected', () async {
      server.listen((request) async {
        request.response.write('<html>login</html>');
        await request.response.close();
      });
      await expectLater(
        client.verify(address, CancelToken()),
        throwsA(isA<VerificationFailure>()),
      );
    });

    test(
      'oversized chunked body without content length is cancelled before EOF',
      () async {
        server.listen((request) async {
          expect(request.response.contentLength, -1);
          request.response.bufferOutput = false;
          request.response.write(' ' * 8192);
          await request.response.flush();
          request.response.write(' ' * 8193);
          await request.response.flush();
          // Deliberately leave the chunked body open: verification must not wait for EOF.
        });
        final token = CancelToken();
        await expectLater(
          client.verify(address, token).timeout(const Duration(seconds: 2)),
          throwsA(
            isA<VerificationFailure>().having(
              (e) => e.message,
              'message',
              contains('oversized'),
            ),
          ),
        );
        expect(token.isCancelled, false);
      },
    );

    test('body limit counts UTF-8 bytes and accepts exactly 16384 bytes', () async {
      var calls = 0;
      server.listen((request) async {
        // Extra fields are allowed; multi-byte characters must count as bytes.
        final body =
            '${statusJson.substring(0, statusJson.length - 1)},"extra":"${'\u00e9' * 5000}"}';
        final padding = 16384 - utf8.encode(body).length;
        request.response.write('$body${' ' * (padding + calls++)}');
        await request.response.close();
      });
      expect((await client.verify(address, CancelToken())).enabled, true);
      await expectLater(
        client.verify(address, CancelToken()),
        throwsA(
          isA<VerificationFailure>().having(
            (e) => e.message,
            'message',
            contains('oversized'),
          ),
        ),
      );
    });

    test('invalid UTF-8 is rejected after bounded reading', () async {
      server.listen((request) async {
        request.response.add([0xff]);
        await request.response.close();
      });
      await expectLater(
        client.verify(address, CancelToken()),
        throwsA(isA<VerificationFailure>()),
      );
    });

    for (final code in [302, 403]) {
      test('HTTP $code rejected without waiting for body EOF', () async {
        server.listen((request) async {
          request.response.statusCode = code;
          request.response.headers.set('location', 'https://example.com/');
          request.response.write('unfinished body');
          await request.response.flush();
        });
        final token = CancelToken();
        await expectLater(
          client.verify(address, token).timeout(const Duration(seconds: 2)),
          throwsA(
            isA<VerificationFailure>().having(
              (e) => e.message,
              'message',
              contains(code == 302 ? 'redirected' : 'unavailable'),
            ),
          ),
        );
        expect(token.isCancelled, false);
      });
    }

    test('cancel while streaming body', () async {
      final arrived = Completer<void>();
      server.listen((request) async {
        request.response.write('{');
        await request.response.flush();
        arrived.complete();
      });
      final token = CancelToken();
      final result = client.verify(address, token);
      final assertion = expectLater(
        result,
        throwsA(
          isA<VerificationFailure>().having(
            (e) => e.message,
            'message',
            contains('cancelled'),
          ),
        ),
      );
      await arrived.future;
      token.cancel();
      await assertion;
    });

    test('total deadline also bounds unfinished body', () async {
      client.close();
      client = StatusClient(deadline: const Duration(milliseconds: 100));
      server.listen((request) async {
        request.response.write('{');
        await request.response.flush();
      });
      await expectLater(
        client
            .verify(address, CancelToken())
            .timeout(const Duration(seconds: 2)),
        throwsA(isA<VerificationFailure>()),
      );
    });

    test('cancel in flight', () async {
      final arrived = Completer<void>();
      server.listen((_) => arrived.complete());
      final token = CancelToken();
      final result = client.verify(address, token);
      final assertion = expectLater(
        result,
        throwsA(
          isA<VerificationFailure>().having(
            (e) => e.message,
            'message',
            contains('cancelled'),
          ),
        ),
      );
      await arrived.future;
      token.cancel();
      await assertion;
    });

    test('total deadline bounds silent server', () async {
      client.close();
      client = StatusClient(deadline: const Duration(milliseconds: 50));
      server.listen((_) {});
      await expectLater(
        client.verify(address, CancelToken()),
        throwsA(isA<VerificationFailure>()),
      );
    });
  });
}
