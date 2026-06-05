// SettingsScreen — minimal first cut: show the signed-in account, the
// server URL, manage mail accounts, signatures, passkey toggle, and a
// Sign-out button. Theme toggle is intentionally system-driven
// (ThemeMode.system in app.dart) so we don't ship a third "App theme"
// preference that drifts from OS dark mode.

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../services/account_service.dart';
import '../../services/auth_service.dart';
import '../../services/passkey_service.dart';
import '../login_screen.dart';
import 'mail_accounts_screen.dart';
import 'signatures_screen.dart';

class SettingsScreen extends StatefulWidget {
  const SettingsScreen({super.key});
  static const route = '/settings';

  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  late final PasskeyService _passkey = context.read<PasskeyService>();

  @override
  Widget build(BuildContext context) {
    final auth = context.watch<AuthService>();
    final accounts = context.watch<AccountService>();
    final account = auth.account;
    final selected = accounts.selected;
    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: ListView(
        children: [
          if (account != null) ...[
            ListTile(
              leading: const Icon(Icons.person_outline),
              title: const Text('Signed in as'),
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
            leading: const Icon(Icons.inbox_outlined),
            title: const Text('Mail accounts'),
            subtitle: Text(selected?.address ?? '${accounts.accounts.length} connected'),
            trailing: const Icon(Icons.chevron_right),
            onTap: () => Navigator.of(context).pushNamed(MailAccountsScreen.route),
          ),
          ListTile(
            leading: const Icon(Icons.draw_outlined),
            title: const Text('HTML signatures'),
            trailing: const Icon(Icons.chevron_right),
            onTap: () => Navigator.of(context).pushNamed(SignaturesScreen.route),
          ),
          SwitchListTile(
            secondary: const Icon(Icons.fingerprint),
            title: const Text('Auto passkey login'),
            subtitle: const Text('Use biometric / device PIN to unlock the app'),
            value: _passkey.enabled,
            onChanged: (v) async {
              await _passkey.setEnabled(v);
              if (mounted) setState(() {});
            },
          ),
          const Divider(),
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
