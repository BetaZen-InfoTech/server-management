// SettingsScreen — minimal first cut: show the signed-in account, the
// server URL, and a Sign-out button. Theme toggle is intentionally
// system-driven (ThemeMode.system in app.dart) so we don't ship a
// third "App theme" preference that drifts from OS dark mode.

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../services/auth_service.dart';
import '../login_screen.dart';

class SettingsScreen extends StatelessWidget {
  const SettingsScreen({super.key});
  static const route = '/settings';

  @override
  Widget build(BuildContext context) {
    final auth = context.watch<AuthService>();
    final account = auth.account;
    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: ListView(
        children: [
          if (account != null) ...[
            ListTile(
              leading: const Icon(Icons.alternate_email),
              title: const Text('Account'),
              subtitle: Text(account.email),
            ),
            ListTile(
              leading: const Icon(Icons.dns_outlined),
              title: const Text('Server'),
              subtitle: Text(account.serverUrl),
            ),
            const Divider(),
          ],
          ListTile(
            leading: Icon(Icons.logout, color: Theme.of(context).colorScheme.error),
            title: Text(
              'Sign out',
              style: TextStyle(color: Theme.of(context).colorScheme.error),
            ),
            onTap: () async {
              await auth.logout();
              if (!context.mounted) return;
              Navigator.of(context).pushNamedAndRemoveUntil(
                LoginScreen.route,
                (_) => false,
              );
            },
          ),
        ],
      ),
    );
  }
}
