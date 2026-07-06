// FcmService — Firebase init, FCM token registration with the mail-suite
// backend (/api/v1/devices), and notification handling in ALL app states:
//   - foreground (onMessage)      → show a local notification
//   - background  (OS tray)       → tap routes to the inbox (onMessageOpenedApp)
//   - terminated  (OS tray)       → tap that launched the app routes to inbox
//                                    (getInitialMessage)
// Plus an explicit high-importance Android channel so OS-displayed
// (background/terminated) notifications show as heads-up on Android 8+.
//
// Requires google-services.json (Android) and GoogleService-Info.plist (iOS).

import 'dart:io' show Platform;

import 'package:firebase_core/firebase_core.dart';
import 'package:firebase_messaging/firebase_messaging.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_local_notifications/flutter_local_notifications.dart';

import '../app/nav.dart';
import '../screens/inbox_screen.dart';
import 'api_client.dart';

// Single source of truth for the notification channel. The backend's FCM
// payload sets android.notification.channel_id = "mail_default", so this id
// MUST match for OS-displayed (background/terminated) notifications to use the
// high-importance channel.
const AndroidNotificationChannel kMailChannel = AndroidNotificationChannel(
  'mail_default',
  'Mail',
  description: 'New mail notifications',
  importance: Importance.high,
);

class FcmService {
  FcmService({required ApiClient api}) : _api = api;

  final ApiClient _api;
  final FlutterLocalNotificationsPlugin _local = FlutterLocalNotificationsPlugin();

  bool _initialised = false;
  String? _lastToken;

  /// Best-effort initialise. Safe to call multiple times; safe to call before
  /// login (token registers lazily once an auth header is available).
  Future<void> init() async {
    if (_initialised) return;
    try {
      await Firebase.initializeApp();
    } catch (e) {
      debugPrint('[fcm] Firebase.initializeApp failed: $e');
      return;
    }

    // Local notifications + foreground-tap routing.
    await _local.initialize(
      const InitializationSettings(
        android: AndroidInitializationSettings('@mipmap/ic_launcher'),
        iOS: DarwinInitializationSettings(),
      ),
      onDidReceiveNotificationResponse: (resp) => _openInbox(),
    );

    // Create the high-importance channel up front so background/terminated
    // notifications (displayed by the OS with channel_id "mail_default") pop as
    // heads-up on Android 8+ instead of being silently dropped/low-importance.
    await _local
        .resolvePlatformSpecificImplementation<AndroidFlutterLocalNotificationsPlugin>()
        ?.createNotificationChannel(kMailChannel);

    final messaging = FirebaseMessaging.instance;
    await messaging.requestPermission(alert: true, badge: true, sound: true);
    // iOS: also surface notifications while the app is foregrounded.
    await messaging.setForegroundNotificationPresentationOptions(
      alert: true,
      badge: true,
      sound: true,
    );

    // Foreground → show a local notification.
    FirebaseMessaging.onMessage.listen(_onForegroundMessage);
    // Background (app alive) → user taps the tray notification.
    FirebaseMessaging.onMessageOpenedApp.listen((_) => _openInbox());
    // Terminated → the notification tap that cold-launched the app.
    final initial = await messaging.getInitialMessage();
    if (initial != null) {
      // Defer until the first frame so the navigator exists.
      WidgetsBinding.instance.addPostFrameCallback((_) => _openInbox());
    }

    // Persist token + register (with retries) now and on rotation.
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

  void _openInbox() {
    rootNavigatorKey.currentState?.pushNamedAndRemoveUntil(
      InboxScreen.route,
      (route) => false,
    );
  }

  Future<void> _registerDevice(String token) async {
    // A couple of short retries covers a transient cold-start blip (e.g. the
    // access token being refreshed by a concurrent request). The api_client now
    // dedupes refreshes, so the retry is belt-and-suspenders.
    for (var attempt = 0; attempt < 3; attempt++) {
      try {
        await _api.post('/api/v1/devices', body: {
          'platform': _platform(),
          'fcm_token': token,
        });
        return; // registered
      } on ApiException catch (e) {
        debugPrint('[fcm] device register attempt ${attempt + 1} failed: ${e.message}');
        if (attempt < 2) {
          await Future<void>.delayed(Duration(milliseconds: 800 * (attempt + 1)));
        }
      }
    }
    // Still failed — we'll try again on the next token refresh / login.
  }

  NotificationDetails _details() => NotificationDetails(
        android: AndroidNotificationDetails(
          kMailChannel.id,
          kMailChannel.name,
          channelDescription: kMailChannel.description,
          importance: Importance.high,
          priority: Priority.high,
          icon: '@mipmap/ic_launcher',
        ),
        iOS: const DarwinNotificationDetails(),
      );

  Future<void> _onForegroundMessage(RemoteMessage m) async {
    final n = m.notification;
    if (n == null) return;
    await _local.show(
      m.hashCode,
      n.title ?? 'New mail',
      n.body ?? '',
      _details(),
      payload: 'inbox',
    );
  }

  /// Shows a local notification immediately — used by Settings → "Send a test
  /// notification" so the user can confirm alerts appear (and look right)
  /// without waiting for a real push. This exercises the exact display path
  /// (channel, importance, icon, tap-routing) the foreground FCM handler uses.
  Future<void> showTestNotification() async {
    try {
      await Firebase.initializeApp();
    } catch (_) {}
    if (!_initialised) await init();
    await _local
        .resolvePlatformSpecificImplementation<AndroidFlutterLocalNotificationsPlugin>()
        ?.requestNotificationsPermission();
    await _local.show(
      99,
      'Betazen Mail',
      '🔔 Notifications are working — new-mail alerts will look like this.',
      _details(),
      payload: 'inbox',
    );
  }

  String _platform() {
    if (Platform.isAndroid) return 'android';
    if (Platform.isIOS) return 'ios';
    return 'web';
  }
}
