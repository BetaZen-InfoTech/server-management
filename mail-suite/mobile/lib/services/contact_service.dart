// ContactService — the address book backing the mobile Contacts screen.
// Mirrors the webmail contract: GET/POST/DELETE /api/v1/contacts.

import 'api_client.dart';

class Contact {
  Contact({required this.id, required this.email, this.name, this.status});

  final String id;
  final String email;
  final String? name;
  final String? status;

  factory Contact.fromJson(Map<String, dynamic> j) => Contact(
        id: j['id']?.toString() ?? '',
        email: j['email']?.toString() ?? '',
        name: (j['name'] as String?)?.trim().isNotEmpty == true ? j['name'] as String : null,
        status: j['status']?.toString(),
      );

  String get display => name ?? email;
}

class ContactService {
  ContactService({required ApiClient api}) : _api = api;
  final ApiClient _api;

  Future<List<Contact>> list({String? search}) async {
    final body = await _api.get('/api/v1/contacts', query: {
      if (search != null && search.isNotEmpty) 'search': search,
      'limit': '200',
    });
    final items = body['items'] ?? body['value'] ?? const <Object?>[];
    if (items is! List) return const [];
    return items.whereType<Map<String, dynamic>>().map(Contact.fromJson).toList();
  }

  Future<void> create({required String email, String? name}) async {
    await _api.post('/api/v1/contacts', body: {
      'email': email,
      'name': name ?? '',
      'status': 'subscribed',
      'group_ids': <String>[],
    });
  }

  Future<void> delete(String id) => _api.delete('/api/v1/contacts/$id');
}
