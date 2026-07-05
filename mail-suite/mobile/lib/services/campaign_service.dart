// CampaignService — read-only view of the user's email campaigns for the
// mobile Campaigns screen. Mirrors the webmail contract:
//   GET /api/v1/campaigns            (list)
//   GET /api/v1/campaigns/:id/stats  (open/click analytics)
//   POST /api/v1/campaigns/:id/{start,pause,cancel}

import 'api_client.dart';

class Campaign {
  Campaign({
    required this.id,
    required this.name,
    required this.status,
    required this.mode,
    required this.subject,
    required this.total,
    required this.sent,
  });

  final String id;
  final String name;
  final String status; // draft | sending | paused | sent | canceled | failed
  final String mode; // now | drip
  final String subject;
  final int total;
  final int sent;

  int get pct => total > 0 ? ((sent / total) * 100).round() : 0;

  factory Campaign.fromJson(Map<String, dynamic> j) => Campaign(
        id: j['id']?.toString() ?? '',
        name: (j['name'] as String?)?.trim().isNotEmpty == true ? j['name'] as String : '(untitled)',
        status: j['status']?.toString() ?? 'draft',
        mode: j['mode']?.toString() ?? 'now',
        subject: j['subject']?.toString() ?? '',
        total: (j['total_recipients'] as num?)?.toInt() ?? 0,
        sent: (j['sent_count'] as num?)?.toInt() ?? 0,
      );
}

class CampaignStats {
  CampaignStats({required this.sent, required this.delivered, required this.opened, required this.clicked});
  final int sent, delivered, opened, clicked;
  factory CampaignStats.fromJson(Map<String, dynamic> j) => CampaignStats(
        sent: (j['sent'] as num?)?.toInt() ?? 0,
        delivered: (j['delivered'] as num?)?.toInt() ?? 0,
        opened: (j['opened'] as num?)?.toInt() ?? 0,
        clicked: (j['clicked'] as num?)?.toInt() ?? 0,
      );
}

class CampaignService {
  CampaignService({required ApiClient api}) : _api = api;
  final ApiClient _api;

  Future<List<Campaign>> list() async {
    final body = await _api.get('/api/v1/campaigns');
    final items = body['value'] ?? body['items'] ?? const <Object?>[];
    if (items is! List) return const [];
    return items.whereType<Map<String, dynamic>>().map(Campaign.fromJson).toList();
  }

  Future<CampaignStats> stats(String id) async {
    final body = await _api.get('/api/v1/campaigns/$id/stats');
    return CampaignStats.fromJson(body);
  }

  Future<void> pause(String id) => _api.post('/api/v1/campaigns/$id/pause');
  Future<void> resume(String id) => _api.post('/api/v1/campaigns/$id/start');
  Future<void> cancel(String id) => _api.post('/api/v1/campaigns/$id/cancel');
}
