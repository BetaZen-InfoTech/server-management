// FcmService — Firebase init, FCM token registration with the
// mail-suite backend (/api/v1/devices), and a foreground hook that
// surfaces incoming push notifications via flutter_local_notifications.
//
// Requires google-services.json (Android) and GoogleService-Info.plist
// (iOS) to be dropped into the respective app folders. The first run
// without them will throw at Firebase.initializeApp — caught here so
// the rest of the app keeps working.

import 'dart:io' show Platform;

import 'package:firebase_core/firebase_core.dart';
import 'package:firebase_messaging/firebase_messaging.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_local_notifications/flutter_local_notifications.dart';

import 'api_client.dart';

class FcmService {
  FcmService({required ApiClient api}) : _api = api;

  final ApiClient _api;
  final FlutterLocalNotificationsPlugin _local = FlutterLocalNotificationsPlugin();

  bool _initialised = false;
  String? _lastToken;

  /// Best-effort initialise. Safe to call multiple times; safe to call
  /// before login (token will register lazily once an auth header is
  /// available).
  Future<void> init() async {
    if (_initialised) return;
    try {
      await Firebase.initializeApp();
    } catch (e) {
      debugPrint('[fcm] Firebase.initializeApp failed: $e');
      return;
    }

    await _local.initialize(const InitializationSettings(
      android: AndroidInitializationSettings('@mipmap/ic_launcher'),
      iOS: DarwinInitializationSettings(),
    ));

    final messaging = FirebaseMessaging.instance;
    await messaging.requestPermission(alert: true, badge: true, sound: true);

    // Foreground: show a local notification so the user sees it even
    // while the app is open.
    FirebaseMessaging.onMessage.listen(_onForegroundMessage);

    // Persist token + register on rotations.
    final token = await messaging.getToken();
    if (token != null) {
      _lastToken = token;
      await _registerDevice(token);
    }
    messaging.onTokenRefresh.listen((t) async {
      _lastToken = t;
      await _registerDevice(t);
    });

    _initialised = true;
  }

  /// Re-register if the user just logged in and we have a token.
  Future<void> registerIfReady() async {
    if (_lastToken != null) await _registerDevice(_lastToken!);
  }

  Future<void> _registerDevice(String token) async {
    try {
      await _api.post('/api/v1/devices', body: {
        'platform': _platform(),
        'fcm_token': token,
      });
    } on ApiException catch (e) {
      // Not signed in yet, or backend doesn't accept the token yet —
      // we'll retry on next token refresh / next login.
      debugPrint('[fcm] device register failed: ${e.message}');
    }
  }

  Future<void> _onForegroundMessage(RemoteMessage m) async {
    final n = m.notification;
    if (n == null) return;
    await _local.show(
      m.hashCode,
      n.title ?? 'New mail',
      n.body ?? '',
      const NotificationDetails(
        android: AndroidNotificationDetails(
          'mail_default',
          'Mail',
          channelDescription: 'New mail notifications',
          importance: Importance.high,
          priority: Priority.high,
        ),
        iOS: DarwinNotificationDetails(),
      ),
    );
  }

  String _platform() {
    if (Platform.isAndroid) return 'android';
    if (Platform.isIOS) return 'ios';
    return 'web';
  }
}
