import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:at_mobile/auth.dart';
import 'package:at_mobile/auth_models.dart';
import 'package:at_mobile/chat.dart';
import 'package:at_mobile/chat_models.dart';
import 'package:at_mobile/chat_stream.dart';
import 'package:at_mobile/server.dart';
import 'package:dio/dio.dart';
import 'package:flutter_test/flutter_test.dart';

import 'auth_fixtures.dart';
import 'chat_fixtures.dart';

String goJson(Object? value) =>
    jsonEncode(value)
        .replaceAll('<', r'\u003c')
        .replaceAll('>', r'\u003e')
        .replaceAll('&', r'\u0026');

class CountingParent extends CancelToken {
  int registrations = 0;
  @override
  Future<DioException> get whenCancel {
    registrations++;
    return super.whenCancel;
  }
}

class WireFixture {
  late final HttpServer server;
  late final StatusClient client;
  late final MobileSession session;
  late final PersonalChatApi api;
  late final Map<String, dynamic> tokens;
  late final ServerAddress address;
  Future<void> Function(HttpRequest)? handle;

  Future<void> start({Duration deadline = const Duration(seconds: 5)}) async {
    server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    address = ServerAddress.parse(
      'http://127.0.0.1:${server.port}/at/',
      allowLocalHttp: true,
    );
    tokens = {
      ...tokenJson(server: address),
      'identity': {
        ...tokenJson()['identity'] as Map<String, dynamic>,
        'roles': ['admin'],
      },
    };
    server.listen((request) async {
      try {
        if (request.uri.path.endsWith('/auth/me')) {
          request.response.write(goJson(meJson(tokens)));
          await request.response.close();
        } else {
          await handle!(request);
        }
      } on SocketException {
        /* Requests intentionally abort oversized/unfinished bodies. */
      } on HttpException {
        /* A cancelled child may close while the server flushes. */
      }
    });
    client = StatusClient(deadline: deadline);
    final vault = FakeVault()..values[address.issuer] = jsonEncode(tokens);
    session = MobileSession(
      MobileAuth.parse(descriptor(address), address),
      transport: client,
      store: vault,
      browser: FakeBrowser(),
      clock: () => authNow,
    );
    await session.restore();
    api = PersonalChatApi(session);
  }

  Future<void> close() async {
    session.dispose();
    client.close();
    await server.close(force: true);
  }
}

void main() {
  test('rename 409 leaves parent reusable for reload and registers one cancellation guard', () async {
    final wire = WireFixture();
    await wire.start();
    addTearDown(wire.close);
    wire.handle = (request) async {
      if (request.method == 'PATCH') {
        request.response.statusCode = 409;
      } else {
        request.response.write(goJson(conversationJson()));
      }
      await request.response.close();
    };
    final parent = CountingParent();
    await expectLater(
      wire.api.rename(cid(1), 'Rename', cancel: parent),
      throwsA(isA<HttpFailure>()),
    );
    expect(parent.isCancelled, false);
    for (var i = 0; i < 12; i++) {
      expect((await wire.api.conversation(cid(1), cancel: parent)).id, cid(1));
    }
    expect(parent.registrations, 1);
    parent.cancel();
    await expectLater(
      wire.api.conversation(cid(1), cancel: parent),
      throwsA(isA<VerificationFailure>()),
    );
  });

  test('controller poll 503 and timeout do not poison recovery or cancellation lifetime', () async {
    final wire = WireFixture();
    await wire.start(deadline: const Duration(milliseconds: 150));
    addTearDown(wire.close);
    var mode = 'ok';
    final snapshot = messageJson(content: 'Saved partial', status: 'streaming');
    wire.handle = (request) async {
      if (request.uri.path.endsWith('/cancel')) {
        request.response.write(
          goJson({
            ...snapshot,
            'status': 'cancelled',
            'error': 'generation_cancelled',
          }),
        );
      } else if (request.uri.path.endsWith('/messages/${cid(3)}')) {
        if (mode == '503') {
          request.response.statusCode = 503;
        } else if (mode == 'timeout') {
          request.response.write('{');
          await request.response.flush();
          return;
        } else {
          request.response.write(goJson(snapshot));
        }
      } else if (request.uri.path.endsWith('/messages')) {
        request.response.write(
          goJson({
            'items': [snapshot],
            'next_before': '',
          }),
        );
      } else {
        request.response.write(goJson(conversationJson()));
      }
      await request.response.close();
    };
    final controller = ConversationController(
      wire.api,
      Conversation.parse(conversationJson(), '01USER'),
      pollInterval: const Duration(hours: 1),
      recoveryLimit: const Duration(hours: 2),
    );
    addTearDown(controller.dispose);
    await controller.load();
    mode = '503';
    await controller.recover();
    expect(controller.error, contains('unavailable'));
    mode = 'timeout';
    await controller.recover();
    expect(controller.activeId, cid(3));
    mode = 'ok';
    await controller.recover();
    expect(controller.messages.single.content, 'Saved partial');
    expect(controller.error, contains('still active'));
    await controller.cancelReply();
    expect(controller.messages.single.status, 'cancelled');
    expect(controller.activeId, isNull);
  });

  test(
    'explicit parent cancel propagates to every active JSON and SSE child',
    () async {
      final wire = WireFixture();
      await wire.start();
      addTearDown(wire.close);
      final allArrived = Completer<void>();
      var arrivals = 0;
      wire.handle = (request) async {
        if (request.method == 'POST') {
          request.response.headers.contentType = ContentType(
            'text',
            'event-stream',
          );
        }
        request.response.write(
          request.method == 'POST' ? ': waiting\n\n' : '{',
        );
        await request.response.flush();
        if (++arrivals == 3) allArrived.complete();
      };
      final parent = CountingParent();
      final first = expectLater(
        wire.client.requestJson(
          wire.address.uri.resolve('one'),
          cancel: parent,
        ),
        throwsA(isA<VerificationFailure>()),
      );
      final second = expectLater(
        wire.client.requestJson(
          wire.address.uri.resolve('two'),
          cancel: parent,
        ),
        throwsA(isA<VerificationFailure>()),
      );
      final stream = expectLater(
        wire.client
            .requestStream(
              wire.address.uri.resolve('three'),
              data: {},
              bearer: secret(1),
              cancel: parent,
            )
            .drain<void>(),
        throwsA(isA<VerificationFailure>()),
      );
      await allArrived.future.timeout(const Duration(seconds: 2));
      expect(parent.registrations, 1);
      parent.cancel('leave screen');
      await Future.wait([first, second, stream])
          .timeout(const Duration(seconds: 2));
      expect(parent.isCancelled, true);
    },
  );

  for (final text in ['<' * 350000, '\x01' * maxAssistantBytes]) {
    for (final terminal in [true, false]) {
      test(
        'escaped ${text.length}-byte text supports ${terminal ? 'terminal SSE' : 'EOF recovery GET'}',
        () async {
          final wire = WireFixture();
          await wire.start();
          addTearDown(wire.close);
          Map<String, dynamic>? saved;
          wire.handle = (request) async {
            if (request.method == 'POST') {
              final body = jsonDecode(
                await utf8.decoder.bind(request).join(),
              ) as Map<String, dynamic>;
              final user = messageJson(
                n: 2,
                role: 'user',
                content: body['content'] as String,
                request: body['request_id'] as String,
                status: 'completed',
              );
              final assistant = messageJson(
                request: body['request_id'] as String,
              );
              saved = {
                ...assistant,
                'content': text,
                'status': 'completed',
                'finish_reason': 'stop',
              };
              expect(
                utf8.encode(goJson(saved)).length,
                greaterThan(2 * 1024 * 1024),
              );
              request.response.headers.contentType = ContentType(
                'text',
                'event-stream',
              );
              request.response.write(
                'event: accepted\ndata: ${goJson({'user': user, 'assistant': assistant, 'replay': false})}\n\n',
              );
              if (terminal) {
                request.response.write(
                  'event: done\ndata: ${goJson(saved)}\n\n',
                );
              }
            } else if (request.uri.path.endsWith('/messages/${cid(3)}')) {
              request.response.write(goJson(saved));
            } else if (request.uri.path.endsWith('/messages')) {
              expect(
                request.uri.queryParameters['limit'],
                '$chatMessagePageSize',
              );
              request.response.write(goJson({'items': [], 'next_before': ''}));
            } else {
              request.response.write(goJson(conversationJson()));
            }
            await request.response.close();
          };
          final controller = ConversationController(
            wire.api,
            Conversation.parse(conversationJson(), '01USER'),
          );
          addTearDown(controller.dispose);
          await controller.load();
          await controller.send('Hi');
          expect(controller.messages.last.content, text);
          expect(controller.messages.last.status, 'completed');
          expect(controller.pending, isNull);
          expect(controller.error, isNull);
        },
      );
    }
  }

  test(
    'two-message page accepts both maximum six-times escaped snapshots',
    () async {
      final wire = WireFixture();
      await wire.start();
      addTearDown(wire.close);
      final text = '\x01' * maxAssistantBytes;
      wire.handle = (request) async {
        expect(request.uri.queryParameters['limit'], '2');
        request.response.write(
          goJson({
            'items': [
              messageJson(n: 4, content: text, status: 'completed'),
              messageJson(n: 3, content: text, status: 'completed'),
            ],
            'next_before': cid(3),
          }),
        );
        await request.response.close();
      };
      final page = await wire.api.messages(cid(1));
      expect(page.items.length, 2);
      expect(page.items.every((m) => m.content == text), true);
      expect(page.nextBefore, cid(3));
    },
  );

  test('truly oversized wire and decoded content remain rejected without poisoning parent', () async {
    final wire = WireFixture();
    await wire.start();
    addTearDown(wire.close);
    wire.handle = (request) async {
      request.response.write(' ' * (maxChatMessageWireBytes + 1));
      await request.response.flush();
    };
    final parent = CancelToken();
    await expectLater(
      wire.api.message(cid(1), cid(3), cancel: parent),
      throwsA(isA<VerificationFailure>()),
    );
    expect(parent.isCancelled, false);
    wire.handle = (request) async {
      request.response.write(goJson(messageJson(status: 'completed')));
      await request.response.close();
    };
    expect(
      (await wire.api.message(cid(1), cid(3), cancel: parent)).terminal,
      true,
    );
    await expectLater(
      parseChatEvents(
        Stream.value(
          utf8.encode(
            'event: unknown\ndata: ${'x' * (maxChatMessageWireBytes + 1)}\n\n',
          ),
        ),
      ).drain<void>(),
      throwsA(isA<VerificationFailure>()),
    );
    expect(
      () => ChatMessage.parse(
        messageJson(content: '\x01' * (maxAssistantBytes + 1)),
        cid(1),
      ),
      throwsA(isA<VerificationFailure>()),
    );
  });

  test(
    'positive sequence is required and retained through deltas and snapshots',
    () {
      for (final order in [null, 0, -1, 1.5, '2']) {
        expect(
          () =>
              ChatMessage.parse({...messageJson(), 'sequence': order}, cid(1)),
          throwsA(isA<VerificationFailure>()),
        );
      }
      final turn = ChatTurn(cid(1), 'request-1', 'Hi');
      turn.apply(
        ChatEvent('accepted', {
          'user': messageJson(
            n: 90,
            sequence: 1,
            role: 'user',
            status: 'completed',
            content: 'Hi',
          ),
          'assistant': messageJson(n: 10, sequence: 2),
          'replay': false,
        }),
      );
      turn.apply(
        ChatEvent('delta', {
          'assistant_message_id': cid(10),
          'offset': 0,
          'content': 'Delta',
        }),
      );
      expect(turn.assistant!.sequence, 2);
      expect(
        () => turn.apply(
          ChatEvent(
            'done',
            messageJson(n: 10, sequence: 3, status: 'completed'),
          ),
        ),
        throwsA(isA<VerificationFailure>()),
      );
      turn.apply(
        ChatEvent('done', messageJson(n: 10, sequence: 2, status: 'completed')),
      );
      expect(turn.terminalEvent, true);
    },
  );

  test(
    'history paginates and merges by sequence despite inverted ULIDs',
    () async {
      final backend = DurableChatBackend()
        ..conversations[cid(1)] = conversationJson();
      for (var order = 1; order <= 4; order++) {
        final n = 100 - order * 10;
        backend.messages[cid(n)] = messageJson(
          n: n,
          sequence: order,
          content: 'Message $order',
          status: 'completed',
        );
      }
      final transport = ChatTransport(backend);
      final session = await chatSession(transport);
      final controller = ConversationController(
        PersonalChatApi(session),
        Conversation.parse(conversationJson(), '01USER'),
      );
      try {
        await controller.load();
        expect(controller.messages.map((m) => m.sequence), [3, 4]);
        await controller.load(older: true);
        expect(controller.messages.map((m) => m.sequence), [1, 2, 3, 4]);
        expect(controller.messages.map((m) => m.id), [
          cid(90),
          cid(80),
          cid(70),
          cid(60),
        ]);
        expect(controller.error, isNull);
      } finally {
        controller.dispose();
        session.dispose();
        transport.close();
      }
    },
  );
}
