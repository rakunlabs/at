import 'package:at_mobile/app.dart';
import 'package:at_mobile/chat.dart';
import 'package:at_mobile/chat_models.dart';
import 'package:at_mobile/chat_screens.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import 'chat_fixtures.dart';

void main() {
  testWidgets(
    'create, send, reload, rename and delete persistent conversation',
    (tester) async {
      final backend = DurableChatBackend();
      final transport = ChatTransport(backend);
      final session = await chatSession(transport);
      await tester.pumpWidget(
        MaterialApp(home: ConversationsScreen(session: session)),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.text('New conversation'));
      await tester.pumpAndSettle();
      await tester.enterText(
        find.widgetWithText(TextField, 'Conversation title'),
        'Travel',
      );
      await tester.tap(find.text('Choose provider and model'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('gpt-4.1'));
      await tester.pumpAndSettle();
      await tester.ensureVisible(find.text('Create conversation'));
      await tester.tap(find.text('Create conversation'));
      await tester.pumpAndSettle();
      await tester.enterText(
        find.widgetWithText(TextField, 'Message'),
        'Hello from mobile',
      );
      await tester.ensureVisible(find.text('Send message'));
      await tester.tap(find.text('Send message'));
      await tester.pumpAndSettle();
      expect(backend.messages.length, 2);
      expect(find.text('Completed'), findsOneWidget);
      await tester.pageBack();
      await tester.pumpAndSettle();
      await tester.tap(find.text('Travel'));
      await tester.pumpAndSettle();
      expect(find.text('Hello from mobile'), findsOneWidget);
      await tester.pageBack();
      await tester.pumpAndSettle();
      await tester.tap(find.byTooltip('Options for Travel'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Rename'));
      await tester.pumpAndSettle();
      await tester.enterText(find.byType(TextFormField), 'Renamed trip');
      await tester.tap(find.text('Rename'));
      await tester.pumpAndSettle();
      expect(find.text('Renamed trip'), findsOneWidget);
      await tester.tap(find.byTooltip('Options for Renamed trip'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Delete'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Delete'));
      await tester.pumpAndSettle();
      expect(backend.conversations, isEmpty);
      expect(backend.messages, isEmpty);
      await tester.pumpWidget(const SizedBox());
      session.dispose();
      transport.close();
    },
  );

  testWidgets(
    'non-admin account has explanation and direct chat route makes no request',
    (tester) async {
      final transport = ChatTransport(DurableChatBackend(), admin: false);
      final session = await chatSession(transport);
      await tester.pumpWidget(
        MaterialApp(home: ConversationsScreen(session: session)),
      );
      await tester.pumpAndSettle();
      expect(find.text('New conversation'), findsNothing);
      expect(
        find.textContaining('require an administrator account'),
        findsOneWidget,
      );
      expect(transport.calls.length, 1);
      await tester.pumpWidget(const SizedBox());
      session.dispose();
      final second = await chatSession(transport);
      await tester.pumpWidget(
        MaterialApp(home: AccountScreen(session: second)),
      );
      await tester.pumpAndSettle();
      expect(find.text('Personal conversations'), findsNothing);
      expect(
        find.textContaining('This account is not authorized'),
        findsOneWidget,
      );
      await tester.pumpWidget(const SizedBox());
      transport.close();
    },
  );

  for (final size in [const Size(320, 568), const Size(834, 1194)]) {
    for (final brightness in Brightness.values) {
      testWidgets(
        'long transcript and keyboard at $size $brightness 200 percent',
        (tester) async {
          tester.view.physicalSize = size;
          tester.view.devicePixelRatio = 1;
          tester.platformDispatcher.textScaleFactorTestValue = 2;
          addTearDown(tester.view.resetPhysicalSize);
          addTearDown(tester.view.resetDevicePixelRatio);
          addTearDown(tester.view.resetViewInsets);
          addTearDown(tester.platformDispatcher.clearAllTestValues);
          final backend = DurableChatBackend()
            ..conversations[cid(1)] = conversationJson();
          backend.messages[cid(3)] = messageJson(
            content:
                'A long reply that should wrap and remain selectable. ' * 300,
            status: 'completed',
          );
          final transport = ChatTransport(backend);
          final session = await chatSession(transport);
          final controller = ConversationController(
            PersonalChatApi(session),
            Conversation.parse(conversationJson(), '01USER'),
          );
          await tester.pumpWidget(
            MaterialApp(
              theme: ThemeData(brightness: brightness),
              home: ConversationScreen(controller: controller),
            ),
          );
          await tester.pumpAndSettle();
          expect(tester.takeException(), isNull);
          await tester.tap(find.byTooltip('Go to latest message'));
          await tester.pumpAndSettle();
          tester.view.viewInsets = const FakeViewPadding(bottom: 250);
          await tester.pumpAndSettle();
          expect(tester.takeException(), isNull);
          expect(find.byType(SelectableText), findsWidgets);
          await tester.pumpWidget(const SizedBox());
          session.dispose();
          transport.close();
        },
      );
    }
  }
}
