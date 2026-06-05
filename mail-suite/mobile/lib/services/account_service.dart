// AccountService — the list of MailAccounts the signed-in user has
// connected, and which one is currently selected. The InboxScreen and
// MailService both read `selected` to decide which mailbox to query.

import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../models/mail_account.dart';
import 'api_client.dart';

class AccountService extends ChangeNotifier {
  AccountService({required ApiClient api, required SharedPreferences prefs})
      : _api = api,
        _prefs = prefs;

  final ApiClient _api;
  final SharedPreferences _prefs;

  static const _kSelectedId = 'selected_account_id';

  List<MailAccount> _accounts = const [];
  String? _selectedId;
  bool _loading = false;

  List<MailAccount> get accounts => _accounts;
  bool get loading => _loading;

  MailAccount? get selected {
    if (_accounts.isEmpty) return null;
    return _accounts.firstWhere(
      (a) => a.id == _selectedId,
      orElse: () => _accounts.firstWhere(
        (a) => a.isPrimary,
        orElse: () => _accounts.first,
      ),
    );
  }

  Future<void> load() async {
    _loading = true;
    notifyListeners();
    try {
      final body = await _api.get('/api/v1/accounts');
      final raw = body['value'] ?? body['accounts'] ?? <Object?>[];
      if (raw is List) {
        _accounts = raw
            .whereType<Map<String, dynamic>>()
            .map(MailAccount.fromJson)
            .toList();
      }
      _selectedId = _prefs.getString(_kSelectedId);
      if (_selectedId == null && _accounts.isNotEmpty) {
        _selectedId = selected?.id;
        if (_selectedId != null) {
          await _prefs.setString(_kSelectedId, _selectedId!);
        }
      }
    } finally {
      _loading = false;
      notifyListeners();
    }
  }

  Future<void> select(String id) async {
    _selectedId = id;
    await _prefs.setString(_kSelectedId, id);
    notifyListeners();
  }

  Future<void> add({
    required String displayName,
    required String address,
    required String password,
    String provider = 'betazen',
    String? imapHost,
    int? imapPort,
    bool imapSsl = true,
    String? smtpHost,
    int? smtpPort,
    bool smtpSsl = true,
    String? username,
    String? color,
  }) async {
    await _api.post('/api/v1/accounts', body: {
      'display_name': displayName,
      'address': address,
      'password': password,
      'provider': provider,
      if (imapHost != null) 'imap_host': imapHost,
      if (imapPort != null) 'imap_port': imapPort,
      'imap_ssl': imapSsl,
      if (smtpHost != null) 'smtp_host': smtpHost,
      if (smtpPort != null) 'smtp_port': smtpPort,
      'smtp_ssl': smtpSsl,
      if (username != null) 'username': username,
      if (color != null) 'color': color,
    });
    await load();
  }

  Future<void> remove(String id) async {
    await _api.delete('/api/v1/accounts/$id');
    if (_selectedId == id) {
      _selectedId = null;
      await _prefs.remove(_kSelectedId);
    }
    await load();
  }
}
