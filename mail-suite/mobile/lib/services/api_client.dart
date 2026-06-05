// ApiClient — the single HTTP entry point.
//
// Responsibilities:
//   - Resolve the request URL by joining the per-account serverUrl
//     with the called path. The base URL lives in SharedPreferences
//     so a re-login against a different server doesn't require an
//     app reinstall.
//   - Attach the JWT access token (Bearer) when one's stored.
//   - On 401 with a non-empty refresh token, run the refresh dance
//     ONCE, replay the original request with the new access token.
//   - Decode JSON bodies, surface non-2xx responses as ApiException
//     with the server's `error.message` when present so screens can
//     show a friendly toast instead of "HTTP 422".
//
// Deliberately NOT a generic dio interceptor stack — the surface is
// small (a few endpoints) and a manual refresh-then-replay keeps the
// auth flow obvious. Swap up if/when the endpoint count grows.

import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:shared_preferences/shared_preferences.dart';

import 'storage.dart';

class ApiException implements Exception {
  ApiException(this.status, this.message, {this.code});

  final int status;
  final String message;
  final String? code;

  @override
  String toString() => 'ApiException($status${code != null ? " $code" : ""}): $message';
}

class ApiClient {
  ApiClient({required SharedPreferences prefs, required SecureStorage storage})
      : _prefs = prefs,
        _storage = storage;

  final SharedPreferences _prefs;
  final SecureStorage _storage;
  final http.Client _client = http.Client();

  static const _kServerUrl = 'server_url';

  String? get serverUrl => _prefs.getString(_kServerUrl);

  Future<void> setServerUrl(String url) async {
    // Trim trailing slash so path joins land cleanly regardless of how
    // the operator typed the host.
    final normalised = url.trim().replaceAll(RegExp(r'/+$'), '');
    await _prefs.setString(_kServerUrl, normalised);
  }

  Future<void> clearServerUrl() => _prefs.remove(_kServerUrl);

  /// Public surface — the four methods screens actually call.
  Future<Map<String, dynamic>> get(String path, {Map<String, String>? query}) =>
      _send('GET', path, query: query);

  Future<Map<String, dynamic>> post(String path, {Object? body}) =>
      _send('POST', path, body: body);

  Future<Map<String, dynamic>> put(String path, {Object? body}) =>
      _send('PUT', path, body: body);

  Future<Map<String, dynamic>> delete(String path) => _send('DELETE', path);

  // ── internals ────────────────────────────────────────────────────

  Future<Map<String, dynamic>> _send(
    String method,
    String path, {
    Object? body,
    Map<String, String>? query,
    bool isRetry = false,
  }) async {
    final base = serverUrl;
    if (base == null || base.isEmpty) {
      throw ApiException(0, 'Server URL not set. Sign in first.');
    }
    final uri = Uri.parse('$base$path').replace(queryParameters: query);
    final headers = <String, String>{
      'Accept': 'application/json',
      if (body != null) 'Content-Type': 'application/json',
    };
    final token = await _storage.readAccessToken();
    if (token != null && token.isNotEmpty) {
      headers['Authorization'] = 'Bearer $token';
    }

    final req = http.Request(method, uri)
      ..headers.addAll(headers);
    if (body != null) {
      req.body = body is String ? body : jsonEncode(body);
    }
    final streamed = await _client.send(req);
    final resp = await http.Response.fromStream(streamed);

    // 401 → try a refresh once, then replay. Skips retry on the refresh
    // endpoint itself so a bad refresh token doesn't infinite-loop.
    if (resp.statusCode == 401 && !isRetry && path != '/api/v1/auth/refresh') {
      final refreshed = await _attemptRefresh();
      if (refreshed) {
        return _send(method, path, body: body, query: query, isRetry: true);
      }
    }

    return _decode(resp);
  }

  Future<bool> _attemptRefresh() async {
    final refresh = await _storage.readRefreshToken();
    if (refresh == null || refresh.isEmpty) return false;
    try {
      final body = await _send(
        'POST',
        '/api/v1/auth/refresh',
        body: {'refresh_token': refresh},
        isRetry: true,
      );
      final access = body['access_token'] as String?;
      final newRefresh = body['refresh_token'] as String?;
      if (access == null || newRefresh == null) return false;
      await _storage.writeTokens(accessToken: access, refreshToken: newRefresh);
      return true;
    } on ApiException {
      return false;
    }
  }

  Map<String, dynamic> _decode(http.Response resp) {
    Map<String, dynamic> body;
    try {
      body = resp.body.isEmpty
          ? <String, dynamic>{}
          : jsonDecode(resp.body) as Map<String, dynamic>;
    } catch (_) {
      // The Fiber backend uses pkg/response on success and
      // {"success": false, "error": {...}} on failure. A non-JSON
      // body means we hit nginx/upstream — surface the status.
      throw ApiException(resp.statusCode, 'Unexpected response from server');
    }
    if (resp.statusCode >= 200 && resp.statusCode < 300) {
      // Backend wraps payloads under `data` on success. Unwrap so
      // callers work against the inner shape.
      final data = body['data'];
      if (data is Map<String, dynamic>) return data;
      if (data != null) return {'value': data};
      return body;
    }
    final err = body['error'];
    final message = err is Map ? (err['message']?.toString() ?? 'Request failed') : 'Request failed';
    final code = err is Map ? err['code']?.toString() : null;
    throw ApiException(resp.statusCode, message, code: code);
  }
}
