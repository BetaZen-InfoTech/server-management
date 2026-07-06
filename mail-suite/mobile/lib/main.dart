// Betazen Mail — entry point.
//
// Wires the global Providers (AuthService, AccountService, MailService,
// SignaturesService, FcmService, PasskeyService, app prefs) and hands
// off to the MaterialApp shell. Auth check happens once on cold start
// in AuthService.bootstrap(); the router then sends the operator to
// /inbox or /login based on whether a valid refresh token survived
// the last session.

import 'package:firebase_core/firebase_core.dart';
import 'package:firebase_messaging/firebase_messaging.dart';
import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'app/app.dart';
import 'services/account_service.dart';
import 'services/api_client.dart';
import 'services/auth_service.dart';
import 'services/campaign_service.dart';
import 'services/contact_service.dart';
import 'services/fcm_service.dart';
import 'services/mail_service.dart';
import 'services/passkey_service.dart';
import 'services/signatures_service.dart';
import 'services/storage.dart';

// Background/terminated FCM handler. Must be a top-level function marked with
// vm:entry-point (it runs in its own isolate). Notification messages are shown
// by the OS automatically; this exists so firebase_messaging has a registered
// background handler (avoids the warning + future-proofs data-message handling).
@pragma('vm:entry-point')
Future<void> _firebaseBackgroundHandler(RemoteMessage message) async {
  try {
    await Firebase.initializeApp();
  } catch (_) {/* already initialised in this isolate */}
}

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  // Register the FCM background handler BEFORE runApp (firebase_messaging
  // requirement). Guarded so a missing Firebase config doesn't crash startup.
  try {
    FirebaseMessaging.onBackgroundMessage(_firebaseBackgroundHandler);
  } catch (_) {/* Firebase not configured on this build */}
  final prefs = await SharedPreferences.getInstance();
  final storage = SecureStorage();
  final api = ApiClient(prefs: prefs, storage: storage);
  final auth = AuthService(api: api, storage: storage, prefs: prefs);
  await auth.bootstrap(); // hydrate tokens + run a silent refresh if needed

  final accounts = AccountService(api: api, prefs: prefs);
  final mail = MailService(api: api, accounts: accounts);
  final signatures = SignaturesService(api: api);
  final contacts = ContactService(api: api);
  final campaigns = CampaignService(api: api);
  final fcm = FcmService(api: api);
  final passkey = PasskeyService(prefs: prefs);

  // Late-bound hooks so AuthService can refresh accounts + push FCM
  // token on login without a constructor-time circular dep.
  auth.accountService = accounts;
  auth.fcmService = fcm;

  // Fire-and-forget Firebase init; safe if google-services.json absent.
  unawaited(fcm.init());

  // If we're signed in already, load the mailbox list now so the inbox
  // screen has data to render before the user pokes anything.
  if (auth.isAuthenticated) {
    unawaited(accounts.load().then((_) => fcm.registerIfReady()));
  }

  runApp(
    MultiProvider(
      providers: [
        ChangeNotifierProvider<AuthService>.value(value: auth),
        ChangeNotifierProvider<AccountService>.value(value: accounts),
        Provider<MailService>.value(value: mail),
        Provider<SignaturesService>.value(value: signatures),
        Provider<ContactService>.value(value: contacts),
        Provider<CampaignService>.value(value: campaigns),
        Provider<FcmService>.value(value: fcm),
        Provider<PasskeyService>.value(value: passkey),
        Provider<ApiClient>.value(value: api),
      ],
      child: const BetazenMailApp(),
    ),
  );
}

void unawaited(Future<void> _) {}
