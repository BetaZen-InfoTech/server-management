// MailAccountsScreen — list connected mailboxes and add new ones.

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../../services/account_service.dart';
import '../../services/api_client.dart';

// Provider quick-setup — mirrors the webmail's External IMAP presets so the
// operator picks Gmail/Outlook/Yahoo/Zoho/iCloud and the IMAP/SMTP server
// settings auto-fill (they only type email + password).
class _ImapPreset {
  const _ImapPreset(this.imapHost, this.imapPort, this.imapSsl, this.smtpHost, this.smtpPort, this.smtpSsl, [this.note]);
  final String imapHost;
  final int imapPort;
  final bool imapSsl;
  final String smtpHost;
  final int smtpPort;
  final bool smtpSsl;
  final String? note;
}

const _imapPresets = <String, _ImapPreset>{
  'Gmail': _ImapPreset('imap.gmail.com', 993, true, 'smtp.gmail.com', 465, true,
      'Gmail needs an App Password (2-Step Verification → App passwords), not your normal password.'),
  'Outlook': _ImapPreset('outlook.office365.com', 993, true, 'smtp.office365.com', 587, false),
  'Yahoo': _ImapPreset('imap.mail.yahoo.com', 993, true, 'smtp.mail.yahoo.com', 465, true,
      'Yahoo needs an App Password (Account Security → Generate app password).'),
  'Zoho': _ImapPreset('imap.zoho.com', 993, true, 'smtp.zoho.com', 465, true),
  'iCloud': _ImapPreset('imap.mail.me.com', 993, true, 'smtp.mail.me.com', 587, false,
      'iCloud needs an app-specific password (appleid.apple.com).'),
};

class MailAccountsScreen extends StatelessWidget {
  const MailAccountsScreen({super.key});
  static const route = '/settings/accounts';

  @override
  Widget build(BuildContext context) {
    final accounts = context.watch<AccountService>();
    return Scaffold(
      appBar: AppBar(title: const Text('Mail accounts')),
      floatingActionButton: FloatingActionButton.extended(
        icon: const Icon(Icons.add),
        label: const Text('Add'),
        onPressed: () => _showAddSheet(context),
      ),
      body: accounts.loading
          ? const Center(child: CircularProgressIndicator())
          : ListView.separated(
              itemCount: accounts.accounts.length,
              separatorBuilder: (_, __) => const Divider(height: 1),
              itemBuilder: (_, i) {
                final a = accounts.accounts[i];
                final isSelected = accounts.selected?.id == a.id;
                return ListTile(
                  leading: CircleAvatar(
                    backgroundColor: _parseColor(a.color) ??
                        Theme.of(context).colorScheme.primary,
                    child: Text(
                      a.address.isNotEmpty
                          ? a.address[0].toUpperCase()
                          : '?',
                      style: const TextStyle(color: Colors.white),
                    ),
                  ),
                  title: Text(a.displayName.isEmpty ? a.address : a.displayName),
                  subtitle: Text(a.address),
                  trailing: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      if (a.isPrimary)
                        const Padding(
                          padding: EdgeInsets.only(right: 8),
                          child: Chip(label: Text('Primary')),
                        ),
                      if (isSelected)
                        const Icon(Icons.check, color: Colors.green)
                      else
                        TextButton(
                          onPressed: () => accounts.select(a.id),
                          child: const Text('Use'),
                        ),
                      IconButton(
                        icon: const Icon(Icons.delete_outline),
                        onPressed: () async {
                          try {
                            await accounts.remove(a.id);
                          } on ApiException catch (e) {
                            if (!context.mounted) return;
                            ScaffoldMessenger.of(context).showSnackBar(
                              SnackBar(content: Text(e.message)),
                            );
                          }
                        },
                      ),
                    ],
                  ),
                );
              },
            ),
    );
  }

  static Color? _parseColor(String? hex) {
    if (hex == null || !hex.startsWith('#') || hex.length != 7) return null;
    final v = int.tryParse(hex.substring(1), radix: 16);
    if (v == null) return null;
    return Color(0xFF000000 | v);
  }

  Future<void> _showAddSheet(BuildContext context) async {
    final formKey = GlobalKey<FormState>();
    final display = TextEditingController();
    final address = TextEditingController();
    final password = TextEditingController();
    String provider = 'betazen';
    final imapHost = TextEditingController();
    final imapPort = TextEditingController(text: '993');
    final smtpHost = TextEditingController();
    final smtpPort = TextEditingController(text: '465');
    bool imapSsl = true;
    bool smtpSsl = true;
    String selectedPreset = ''; // '', a provider name, or 'Other'
    String? providerNote;

    await showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      builder: (sheetCtx) {
        return Padding(
          padding: EdgeInsets.only(
            bottom: MediaQuery.of(sheetCtx).viewInsets.bottom,
            left: 16, right: 16, top: 16,
          ),
          child: StatefulBuilder(
            builder: (sheetCtx, setSheetState) => Form(
              key: formKey,
              child: SingleChildScrollView(
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    SegmentedButton<String>(
                      segments: const [
                        ButtonSegment(value: 'betazen', label: Text('Betazen')),
                        ButtonSegment(value: 'imap', label: Text('External IMAP')),
                      ],
                      selected: {provider},
                      onSelectionChanged: (s) =>
                          setSheetState(() => provider = s.first),
                    ),
                    const SizedBox(height: 12),
                    TextFormField(
                      controller: display,
                      decoration: const InputDecoration(labelText: 'Display name'),
                      validator: (v) => (v ?? '').isEmpty ? 'Required' : null,
                    ),
                    TextFormField(
                      controller: address,
                      keyboardType: TextInputType.emailAddress,
                      decoration: const InputDecoration(labelText: 'Email address'),
                      validator: (v) => (v ?? '').contains('@') ? null : 'Email required',
                    ),
                    TextFormField(
                      controller: password,
                      obscureText: true,
                      decoration: const InputDecoration(labelText: 'Password'),
                      validator: (v) => (v ?? '').isEmpty ? 'Required' : null,
                    ),
                    if (provider == 'imap') ...[
                      const SizedBox(height: 12),
                      const Align(
                        alignment: Alignment.centerLeft,
                        child: Text('Quick setup — pick your provider, then just enter the email + password',
                            style: TextStyle(fontSize: 12)),
                      ),
                      const SizedBox(height: 6),
                      Wrap(
                        spacing: 8,
                        runSpacing: 0,
                        children: [
                          for (final name in _imapPresets.keys)
                            ChoiceChip(
                              label: Text(name),
                              selected: selectedPreset == name,
                              onSelected: (_) => setSheetState(() {
                                selectedPreset = name;
                                final p = _imapPresets[name]!;
                                imapHost.text = p.imapHost;
                                imapPort.text = '${p.imapPort}';
                                imapSsl = p.imapSsl;
                                smtpHost.text = p.smtpHost;
                                smtpPort.text = '${p.smtpPort}';
                                smtpSsl = p.smtpSsl;
                                providerNote = p.note;
                              }),
                            ),
                          ChoiceChip(
                            label: const Text('Other (manual)'),
                            selected: selectedPreset == 'Other',
                            onSelected: (_) => setSheetState(() {
                              selectedPreset = 'Other';
                              imapHost.text = '';
                              smtpHost.text = '';
                              providerNote = null;
                            }),
                          ),
                        ],
                      ),
                      if (providerNote != null)
                        Padding(
                          padding: const EdgeInsets.symmetric(vertical: 6),
                          child: Text(providerNote!,
                              style: TextStyle(fontSize: 12, color: Theme.of(sheetCtx).colorScheme.primary)),
                        ),
                      const SizedBox(height: 4),
                      TextFormField(controller: imapHost, decoration: const InputDecoration(labelText: 'IMAP host')),
                      TextFormField(controller: imapPort, keyboardType: TextInputType.number, decoration: const InputDecoration(labelText: 'IMAP port')),
                      SwitchListTile(
                        contentPadding: EdgeInsets.zero,
                        title: const Text('IMAP SSL'),
                        value: imapSsl,
                        onChanged: (v) => setSheetState(() {
                          imapSsl = v;
                          imapPort.text = v ? '993' : '143';
                        }),
                      ),
                      TextFormField(controller: smtpHost, decoration: const InputDecoration(labelText: 'SMTP host')),
                      TextFormField(controller: smtpPort, keyboardType: TextInputType.number, decoration: const InputDecoration(labelText: 'SMTP port')),
                      SwitchListTile(
                        contentPadding: EdgeInsets.zero,
                        title: const Text('SMTP SSL'),
                        value: smtpSsl,
                        onChanged: (v) => setSheetState(() {
                          smtpSsl = v;
                          smtpPort.text = v ? '465' : '587';
                        }),
                      ),
                    ],
                    const SizedBox(height: 12),
                    SizedBox(
                      width: double.infinity,
                      child: FilledButton(
                        onPressed: () async {
                          if (!(formKey.currentState?.validate() ?? false)) return;
                          try {
                            await context.read<AccountService>().add(
                                  displayName: display.text.trim(),
                                  address: address.text.trim(),
                                  password: password.text,
                                  provider: provider,
                                  imapHost: provider == 'imap' ? imapHost.text.trim() : null,
                                  imapPort: provider == 'imap' ? int.tryParse(imapPort.text) : null,
                                  imapSsl: imapSsl,
                                  smtpHost: provider == 'imap' ? smtpHost.text.trim() : null,
                                  smtpPort: provider == 'imap' ? int.tryParse(smtpPort.text) : null,
                                  smtpSsl: smtpSsl,
                                );
                            if (sheetCtx.mounted) Navigator.of(sheetCtx).pop();
                          } on ApiException catch (e) {
                            if (sheetCtx.mounted) {
                              ScaffoldMessenger.of(sheetCtx).showSnackBar(
                                SnackBar(content: Text(e.message)),
                              );
                            }
                          }
                        },
                        child: const Text('Add mailbox'),
                      ),
                    ),
                    const SizedBox(height: 16),
                  ],
                ),
              ),
            ),
          ),
        );
      },
    );
  }
}
