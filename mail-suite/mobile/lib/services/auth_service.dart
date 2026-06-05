// AuthService — login / refresh / logout, plus the source of truth for
// "is the operator signed in" the router listens to.
//
// Token contract MUST match the panel: snake_case `access_token` +
// `refresh_token`. The Betazen panel went through a critical bug fix
// over this once (CLAUDE.md note); the mail-suite backend mirrors the
// same shape so this client targets the same field names.

import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../models/account.dart';
import 'account_service.dart';
import 'api_client.dart';
import 'fcm_service.dart';
import 'storage.dart';

class AuthService extends ChangeNotifier {
  AuthService({
    required ApiClient api,
    required SecureStorage storage,
    required SharedPreferences prefs,
  })  : _api = api,
        _storage = storage,
        _prefs = prefs;

  final ApiClient _api;
  final SecureStorage _storage;
  final SharedPreferences _prefs;

  /// Optional hooks: set by main.dart after construction so this
  /// service can refresh dependent state on login/logout without
  /// taking a hard dependency on those services in its constructor
  /// (which would create a cycle — AccountService talks to ApiClient
  /// which surfaces 401s back through AuthService.refresh).
  AccountService? accountService;
  FcmService? fcmService;

  Account? _account;
  bool _hasToken = false;

  Account? get account => _account;
  bool get isAuthenticated => _hasToken && _account != null;

  static const _kAccountJson = 'account_json';

  /// Cold-start hydration. Reads the cached account + tests whether a
  /// stored access token exists. Doesn't proactively refresh — the
  /// first real API call will trip the 401 path and refresh then.
  Future<void> bootstrap() async {
    final raw = _prefs.getString(_kAccountJson);
    if (raw != null && raw.isNotEmpty) {
      try {
        final decoded = jsonDecode(raw);
        if (decoded is Map<String, dynamic>) {
          _account = Account.fromJson(decoded);
        }
      } catch (_) {
        // Corrupt prefs entry — clear it so we don't loop on the splash.
        await _prefs.remove(_kAccountJson);
      }
    }
    final token = await _storage.readAccessToken();
    _hasToken = token != null && token.isNotEmpty;
    notifyListeners();
  }

  Future<void> login({
    required String serverUrl,
    required String email,
    required String password,
  }) async {
    await _api.setServerUrl(serverUrl);
    final body = await _api.post(
      '/api/v1/auth/login',
      body: {'email': email, 'password': password},
    );
    final access = body['access_token'] as String?;
    final refresh = body['refresh_token'] as String?;
    if (access == null || refresh == null) {
      throw ApiException(0, 'Server response missing tokens');
    }
    await _storage.writeTokens(accessToken: access, refreshToken: refresh);
    // Backend may surface a friendly display_name either at the top
    // level or nested under a `user` object — accept either to stay
    // ahead of the backend's still-evolving shape.
    final displayName = (body['display_name'] as String?) ??
        (body['user'] is Map ? (body['user']['display_name'] as String?) : null);
    _account = Account(email: email, serverUrl: serverUrl, displayName: displayName);
    _hasToken = true;
    await _prefs.setString(_kAccountJson, jsonEncode(_account!.toJson()));
    notifyListeners();
    // Best-effort: pull mailbox list + push FCM token. Failures here
    // are non-fatal — the user is signed in.
    try {
      await accountService?.load();
      await fcmService?.registerIfReady();
    } catch (_) {/* ignored */}
  }

  Future<void> logout() async {
    // Best-effort backend revoke; ignore failures so a network drop
    // doesn't trap the operator signed in.
    try {
      await _api.post('/api/v1/auth/logout');
    } catch (_) {/* ignored */}
    await _storage.clearTokens();
    await _prefs.remove(_kAccountJson);
    _account = null;
    _hasToken = false;
    notifyListeners();
  }
}
