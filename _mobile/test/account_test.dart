import 'dart:async';
import 'dart:convert';

import 'package:at_mobile/app.dart';
import 'package:at_mobile/auth.dart';
import 'package:at_mobile/server.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'auth_fixtures.dart';

class AccountStatus extends FakeAuthTransport {
  @override
  Future<AuthStatus> verify(ServerAddress server, CancelToken cancel) async =>
      AuthStatus(
        enabled: true,
        passkeys: true,
        rememberMe: true,
        passkeyLogin: 'username-first',
        mobileAuth: descriptor(server),
      );
}

class AccountPreferences extends ServerPreferences {
  @override
  Future<String?> load() async => authServer.toString();
  @override
  Future<void> save(ServerAddress server) async {}
}

void main() {
  late AccountStatus transport;
  late FakeVault vault;
  late FakeBrowser browser;
  setUp(() {
    transport = AccountStatus();
    vault = FakeVault();
    browser = FakeBrowser();
    transport.handler = (request) async {
      if (request.url.path.endsWith('/me')) return meJson(tokenJson());
      if (request.url.path.endsWith('/begin')) {
        return {
          'authorization_url':
              '${authServer.uri}#/mobile-authorize?request_id=${secret(3)}',
          'expires_in': 300,
        };
      }
      if (request.url.path.endsWith('/token')) return tokenJson();
      return null;
    };
    browser.handler = (_) async =>
        '$callbackUriForTest?code=${secret(6)}&state=${transport.calls.first.data!['state']}';
  });
  tearDown(() => transport.close());

  Future<void> connect(WidgetTester tester) async {
    await tester.pumpWidget(
      ATApp(
        client: transport,
        preferences: AccountPreferences(),
        sessionFactory: (discovery) => MobileSession(
          discovery,
          transport: transport,
          store: vault,
          browser: browser,
          clock: () => authNow,
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.text('Verify server'));
    await tester.tap(find.text('Verify server'));
    await tester.pumpAndSettle();
  }

  testWidgets(
    'browser cancellation returns to real sign-in without credentials',
    (tester) async {
      browser.handler = (_) async =>
          throw const VerificationFailure('Browser sign-in cancelled.');
      await connect(tester);
      await tester.ensureVisible(find.text('Sign in with browser'));
      await tester.tap(find.text('Sign in with browser'));
      await tester.pumpAndSettle();
      expect(find.text('Browser sign-in cancelled.'), findsOneWidget);
      expect(find.text('Your account'), findsNothing);
      expect(vault.values, isEmpty);
      expect(transport.calls.length, 1);
    },
  );

  testWidgets(
    'restoring waits for bearer me before showing authenticated account',
    (tester) async {
      vault.values[authServer.issuer] = jsonEncode(tokenJson());
      final gate = Completer<Object?>();
      transport.handler = (_) => gate.future;
      await tester.pumpWidget(
        ATApp(
          client: transport,
          preferences: AccountPreferences(),
          sessionFactory: (discovery) => MobileSession(
            discovery,
            transport: transport,
            store: vault,
            browser: browser,
            clock: () => authNow,
          ),
        ),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.text('Verify server'));
      await tester.pump();
      await tester.pump(const Duration(milliseconds: 400));
      expect(find.text('Restoring secure session...'), findsOneWidget);
      expect(find.text('Your account'), findsNothing);
      gate.complete(meJson(tokenJson()));
      await tester.pumpAndSettle();
      expect(find.text('Your account'), findsOneWidget);
      expect(find.text('reader@example.com'), findsOneWidget);
      expect(find.text('Role: Account holder'), findsOneWidget);
      expect(browser.calls, 0);
    },
  );

  testWidgets(
    'browser approval signs in and switch revokes then clears namespace',
    (tester) async {
      await connect(tester);
      await tester.ensureVisible(find.text('Sign in with browser'));
      await tester.tap(find.text('Sign in with browser'));
      await tester.pumpAndSettle();
      expect(find.text('Your account'), findsOneWidget);
      expect(vault.values.length, 1);
      await tester.ensureVisible(find.text('Sign out and change server'));
      await tester.tap(find.text('Sign out and change server'));
      await tester.pumpAndSettle();
      expect(find.text('Verify server'), findsOneWidget);
      expect(vault.values, isEmpty);
      expect(transport.calls.last.url.path, '/at/auth/mobile/logout');
      expect(transport.calls.last.bearer, isNull);
    },
  );

  for (final size in [const Size(320, 568), const Size(834, 1194)]) {
    for (final brightness in Brightness.values) {
      testWidgets('account large text $size $brightness remains scrollable', (
        tester,
      ) async {
        tester.view.physicalSize = size;
        tester.view.devicePixelRatio = 1;
        tester.platformDispatcher.textScaleFactorTestValue = 2;
        tester.platformDispatcher.platformBrightnessTestValue = brightness;
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        addTearDown(tester.platformDispatcher.clearAllTestValues);
        vault.values[authServer.issuer] = jsonEncode(tokenJson());
        await connect(tester);
        expect(find.text('Your account'), findsOneWidget);
        await tester.ensureVisible(find.text('Sign out and change server'));
        expect(tester.takeException(), isNull);
      });
    }
  }
}

const callbackUriForTest = 'atmobile://auth/callback';
