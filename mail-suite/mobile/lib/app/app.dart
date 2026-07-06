// MaterialApp shell — theme, route table, and the top-level auth
// redirect. Splits routing out of main.dart so the route table can grow
// without bloating the entry point.

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../screens/campaigns_screen.dart';
import '../screens/compose_screen.dart';
import '../screens/contacts_screen.dart';
import '../screens/inbox_screen.dart';
import '../screens/login_screen.dart';
import '../screens/message_screen.dart';
import '../screens/settings/mail_accounts_screen.dart';
import '../screens/settings/settings_screen.dart';
import '../screens/settings/signatures_screen.dart';
import '../services/auth_service.dart';
import 'nav.dart';
import 'theme.dart';

class BetazenMailApp extends StatelessWidget {
  const BetazenMailApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Betazen Mail',
      navigatorKey: rootNavigatorKey,
      debugShowCheckedModeBanner: false,
      theme: buildLightTheme(),
      darkTheme: buildDarkTheme(),
      themeMode: ThemeMode.system,
      // Pick the start route based on whether bootstrap landed us with
      // a usable access token. Subsequent navigation goes through
      // Navigator.pushNamed; this only fires once.
      home: const _RootRouter(),
      routes: {
        LoginScreen.route: (_) => const LoginScreen(),
        InboxScreen.route: (_) => const InboxScreen(),
        SettingsScreen.route: (_) => const SettingsScreen(),
        MailAccountsScreen.route: (_) => const MailAccountsScreen(),
        SignaturesScreen.route: (_) => const SignaturesScreen(),
        ComposeScreen.route: (_) => const ComposeScreen(),
        ContactsScreen.route: (_) => const ContactsScreen(),
        CampaignsScreen.route: (_) => const CampaignsScreen(),
      },
      // MessageScreen needs the message id from arguments; routed via
      // onGenerateRoute so we don't have to thread it through a global
      // map.
      onGenerateRoute: (settings) {
        if (settings.name == MessageScreen.route) {
          final id = settings.arguments as String;
          return MaterialPageRoute(
            settings: settings,
            builder: (_) => MessageScreen(messageId: id),
          );
        }
        return null;
      },
    );
  }
}

class _RootRouter extends StatelessWidget {
  const _RootRouter();

  @override
  Widget build(BuildContext context) {
    // Listen so a logout from any screen kicks back to /login without
    // an explicit Navigator.pushReplacement at the call site.
    final auth = context.watch<AuthService>();
    return auth.isAuthenticated ? const InboxScreen() : const LoginScreen();
  }
}
