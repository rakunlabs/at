import 'dart:async';

import 'package:at_mobile/app.dart';
import 'package:at_mobile/server.dart';
import 'package:dio/dio.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

class MemoryPreferences extends ServerPreferences {
  String? value;
  Completer<void>? pendingSave;
  Completer<void>? pendingClear;
  int saves = 0;
  int clears = 0;
  @override
  Future<String?> load() async => value;
  @override
  Future<void> save(ServerAddress server) async {
    saves++;
    await pendingSave?.future;
    value = server.toString();
  }

  @override
  Future<void> clear() async {
    clears++;
    await pendingClear?.future;
    value = null;
  }
}

class FakeClient extends StatusClient {
  CancelToken? token;
  String? address;
  Completer<AuthStatus>? pending;
  bool fail = false;
  @override
  Future<AuthStatus> verify(ServerAddress server, CancelToken cancel) async {
    token = cancel;
    address = server.toString();
    if (fail) {
      throw const VerificationFailure('Check the server URL and try again.');
    }
    return pending?.future ??
        const AuthStatus(
          enabled: true,
          passkeys: false,
          rememberMe: true,
          passkeyLogin: 'username-first',
        );
  }
}

void main() {
  testWidgets('verify, show unauthenticated summary, change server, forget', (
    tester,
  ) async {
    final client = FakeClient();
    final prefs = MemoryPreferences();
    await tester.pumpWidget(ATApp(client: client, preferences: prefs));
    await tester.pumpAndSettle();
    await tester.enterText(
      find.byType(TextFormField),
      'https://example.com/at',
    );
    await tester.tap(find.text('Verify server'));
    await tester.pumpAndSettle();
    expect(find.text('Server verified'), findsOneWidget);
    expect(find.text('Mobile sign-in not available yet'), findsOneWidget);
    expect(
      find.text('Public status checked. You are not signed in to the app.'),
      findsOneWidget,
    );
    expect(prefs.value, 'https://example.com/at/');
    await tester.pageBack();
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.text('Forget saved server'));
    await tester.tap(find.text('Forget saved server'));
    await tester.pumpAndSettle();
    expect(prefs.value, isNull);
  });

  testWidgets(
    'restore does not verify or authenticate; failed verification is retryable',
    (tester) async {
      final client = FakeClient()..fail = true;
      final prefs = MemoryPreferences()..value = 'https://example.com/';
      await tester.pumpWidget(ATApp(client: client, preferences: prefs));
      await tester.pumpAndSettle();
      expect(client.address, isNull);
      await tester.tap(find.text('Verify server'));
      await tester.pumpAndSettle();
      expect(find.text('Check the server URL and try again.'), findsOneWidget);
      expect(find.text('Server verified'), findsNothing);
      expect(find.text('Verify server'), findsOneWidget);
    },
  );

  testWidgets('cancel discards a late result', (tester) async {
    final client = FakeClient()..pending = Completer<AuthStatus>();
    final prefs = MemoryPreferences();
    await tester.pumpWidget(ATApp(client: client, preferences: prefs));
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextFormField), 'https://example.com');
    await tester.tap(find.text('Verify server'));
    await tester.pump();
    await tester.ensureVisible(find.text('Cancel verification'));
    await tester.tap(find.text('Cancel verification'));
    await tester.pump();
    expect(client.token!.isCancelled, true);
    client.pending!.complete(
      const AuthStatus(
        enabled: true,
        passkeys: false,
        rememberMe: false,
        passkeyLogin: 'username-first',
      ),
    );
    await tester.pumpAndSettle();
    expect(prefs.value, isNull);
    expect(find.text('Server verified'), findsNothing);
  });

  testWidgets(
    'delayed forget blocks input and serializes preference mutations',
    (tester) async {
      final client = FakeClient();
      final prefs = MemoryPreferences()
        ..value = 'https://old.example.com/'
        ..pendingClear = Completer<void>();
      await tester.pumpWidget(ATApp(client: client, preferences: prefs));
      await tester.pumpAndSettle();
      final forget = tester
          .widget<TextButton>(
            find.widgetWithText(TextButton, 'Forget saved server'),
          )
          .onPressed!;
      final verify = tester
          .widget<FilledButton>(find.byType(FilledButton))
          .onPressed!;
      forget();
      // Even callbacks from the previous frame cannot start overlapping writes.
      forget();
      verify();
      await tester.pump();
      expect(prefs.clears, 1);
      expect(client.address, isNull);
      expect(
        tester.widget<TextFormField>(find.byType(TextFormField)).enabled,
        false,
      );
      expect(
        tester.widget<FilledButton>(find.byType(FilledButton)).onPressed,
        isNull,
      );
      expect(find.text('Removing saved server...'), findsOneWidget);
      expect(find.text('Cancel verification'), findsNothing);

      prefs.pendingClear!.complete();
      await tester.pumpAndSettle();
      expect(
        tester
            .widget<TextFormField>(find.byType(TextFormField))
            .controller!
            .text,
        isEmpty,
      );
      await tester.enterText(
        find.byType(TextFormField),
        'https://new.example.com/at',
      );
      await tester.ensureVisible(find.text('Verify server'));
      await tester.tap(find.text('Verify server'));
      await tester.pumpAndSettle();
      expect(prefs.value, 'https://new.example.com/at/');
      expect(prefs.saves, 1);
      expect(find.text('Server verified'), findsOneWidget);
    },
  );

  testWidgets('delayed save enters committing phase and rejects stale cancel', (
    tester,
  ) async {
    final client = FakeClient()..pending = Completer<AuthStatus>();
    final prefs = MemoryPreferences()..pendingSave = Completer<void>();
    await tester.pumpWidget(ATApp(client: client, preferences: prefs));
    await tester.pumpAndSettle();
    final forget = tester
        .widget<TextButton>(
          find.widgetWithText(TextButton, 'Forget saved server'),
        )
        .onPressed!;
    await tester.enterText(
      find.byType(TextFormField),
      'https://example.com/at',
    );
    await tester.tap(find.text('Verify server'));
    await tester.pump();
    final cancel = tester
        .widget<TextButton>(
          find.widgetWithText(TextButton, 'Cancel verification'),
        )
        .onPressed!;
    client.pending!.complete(
      const AuthStatus(
        enabled: true,
        passkeys: false,
        rememberMe: true,
        passkeyLogin: 'username-first',
      ),
    );
    await tester.pump();
    expect(prefs.saves, 1);
    expect(find.text('Saving server...'), findsOneWidget);
    expect(find.text('Cancel verification'), findsNothing);
    expect(
      tester.widget<TextFormField>(find.byType(TextFormField)).enabled,
      false,
    );
    expect(
      tester.widget<FilledButton>(find.byType(FilledButton)).onPressed,
      isNull,
    );
    cancel();
    forget();
    await tester.pump();
    expect(client.token!.isCancelled, false);
    expect(prefs.clears, 0);
    expect(find.text('Verification cancelled.'), findsNothing);
    prefs.pendingSave!.complete();
    await tester.pumpAndSettle();
    expect(prefs.value, 'https://example.com/at/');
    expect(find.text('Server verified'), findsOneWidget);
  });

  for (final size in [const Size(320, 568), const Size(834, 1194)]) {
    for (final brightness in Brightness.values) {
      testWidgets('large text ${size.width} $brightness has no layout errors', (
        tester,
      ) async {
        tester.view.physicalSize = size;
        tester.view.devicePixelRatio = 1;
        tester.platformDispatcher.textScaleFactorTestValue = 2;
        tester.platformDispatcher.platformBrightnessTestValue = brightness;
        addTearDown(tester.view.resetPhysicalSize);
        addTearDown(tester.view.resetDevicePixelRatio);
        addTearDown(tester.platformDispatcher.clearAllTestValues);
        await tester.pumpWidget(
          ATApp(client: FakeClient(), preferences: MemoryPreferences()),
        );
        await tester.pumpAndSettle();
        expect(tester.takeException(), isNull);
        await tester.enterText(
          find.byType(TextFormField),
          'https://example.com/at',
        );
        await tester.ensureVisible(find.text('Verify server'));
        await tester.tap(find.text('Verify server'));
        await tester.pumpAndSettle();
        await tester.ensureVisible(find.text('Change or recheck server'));
        expect(tester.takeException(), isNull);
      });
    }
  }
}
