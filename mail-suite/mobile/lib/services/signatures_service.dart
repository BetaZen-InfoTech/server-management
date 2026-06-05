// SignaturesService — CRUD over /api/v1/signatures. The compose screen
// shows a dropdown of these and the server stitches the chosen HTML
// onto the outgoing message.

import '../models/signature.dart';
import 'api_client.dart';

class SignaturesService {
  SignaturesService({required ApiClient api}) : _api = api;
  final ApiClient _api;

  Future<List<Signature>> list() async {
    final body = await _api.get('/api/v1/signatures');
    final raw = body['value'] ?? body['signatures'] ?? <Object?>[];
    if (raw is! List) return const [];
    return raw
        .whereType<Map<String, dynamic>>()
        .map(Signature.fromJson)
        .toList();
  }

  Future<Signature> create({
    required String name,
    required String html,
    bool isDefault = false,
  }) async {
    final body = await _api.post('/api/v1/signatures', body: {
      'name': name,
      'html': html,
      'is_default': isDefault,
    });
    return Signature.fromJson(body);
  }

  Future<Signature> update(
    String id, {
    required String name,
    required String html,
    bool isDefault = false,
  }) async {
    final body = await _api.put('/api/v1/signatures/$id', body: {
      'name': name,
      'html': html,
      'is_default': isDefault,
    });
    return Signature.fromJson(body);
  }

  Future<void> delete(String id) => _api.delete('/api/v1/signatures/$id');
}
