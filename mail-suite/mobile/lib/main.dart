// Betazen Mail — entry point.
//
// Wires the global Providers (AuthService, MailService, app prefs) and
// hands off to the MaterialApp shell. Auth check happens once on cold
// start in AuthService.bootstrap(); the router then sends the operator
// to /inbox or /login based on whether a valid refresh token survived
// the last session.

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';
import 'package:shared_preferences/shared_preferences.dart';

import 'app/app.dart';
import 'services/api_client.dart';
import 'services/auth_service.dart';
import 'services/mail_service.dart';
import 'services/storage.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final prefs = await SharedPreferences.getInstance();
  final storage = SecureStorage();
  final api = ApiClient(prefs: prefs, storage: storage);
  final auth = AuthService(api: api, storage: storage, prefs: prefs);
  await auth.bootstrap(); // hydrate tokens + run a silent refresh if needed
  final mail = MailService(api: api);

  runApp(
    MultiProvider(
      providers: [
        ChangeNotifierProvider<AuthService>.value(value: auth),
        Provider<MailService>.value(value: mail),
        Provider<ApiClient>.value(value: api),
      ],
      child: const BetazenMailApp(),
    ),
  );
}
