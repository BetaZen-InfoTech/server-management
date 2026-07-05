// ContactsScreen — the mobile address book: list, search, add, delete.
// Brings the webmail's Contacts feature to the app.

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../services/api_client.dart';
import '../services/contact_service.dart';

class ContactsScreen extends StatefulWidget {
  const ContactsScreen({super.key});
  static const route = '/contacts';

  @override
  State<ContactsScreen> createState() => _ContactsScreenState();
}

class _ContactsScreenState extends State<ContactsScreen> {
  List<Contact> _contacts = const [];
  bool _loading = false;
  String? _error;
  String _search = '';

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final list = await context.read<ContactService>().list(search: _search);
      if (mounted) setState(() => _contacts = list);
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.message);
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _add() async {
    final res = await showModalBottomSheet<_NewContact>(
      context: context,
      isScrollControlled: true,
      builder: (_) => const _AddContactSheet(),
    );
    if (res == null) return;
    try {
      await context.read<ContactService>().create(email: res.email, name: res.name);
      if (mounted) ScaffoldMessenger.of(context).showSnackBar(const SnackBar(content: Text('Contact added')));
      await _load();
    } on ApiException catch (e) {
      if (mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(e.message)));
    }
  }

  Future<void> _delete(Contact c) async {
    try {
      await context.read<ContactService>().delete(c.id);
      await _load();
    } on ApiException catch (e) {
      if (mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(e.message)));
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Contacts'),
        bottom: PreferredSize(
          preferredSize: const Size.fromHeight(56),
          child: Padding(
            padding: const EdgeInsets.fromLTRB(12, 0, 12, 8),
            child: TextField(
              decoration: const InputDecoration(
                hintText: 'Search contacts',
                prefixIcon: Icon(Icons.search),
                isDense: true,
                border: OutlineInputBorder(),
              ),
              onSubmitted: (v) {
                _search = v.trim();
                _load();
              },
            ),
          ),
        ),
      ),
      floatingActionButton: FloatingActionButton(
        onPressed: _add,
        child: const Icon(Icons.person_add_alt_1),
      ),
      body: _body(),
    );
  }

  Widget _body() {
    if (_error != null && _contacts.isEmpty) {
      return Center(
        child: Column(mainAxisSize: MainAxisSize.min, children: [
          const Icon(Icons.cloud_off_outlined, size: 48),
          const SizedBox(height: 8),
          Text(_error!),
          const SizedBox(height: 12),
          FilledButton.tonal(onPressed: _load, child: const Text('Retry')),
        ]),
      );
    }
    if (_loading && _contacts.isEmpty) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_contacts.isEmpty) {
      return const Center(child: Text('No contacts yet — add one with +'));
    }
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView.separated(
        itemCount: _contacts.length,
        separatorBuilder: (_, __) => const Divider(height: 1),
        itemBuilder: (_, i) {
          final c = _contacts[i];
          return Dismissible(
            key: ValueKey(c.id),
            direction: DismissDirection.endToStart,
            background: Container(
              color: Colors.red,
              alignment: Alignment.centerRight,
              padding: const EdgeInsets.only(right: 20),
              child: const Icon(Icons.delete, color: Colors.white),
            ),
            confirmDismiss: (_) async {
              return await showDialog<bool>(
                    context: context,
                    builder: (_) => AlertDialog(
                      title: Text('Delete ${c.email}?'),
                      actions: [
                        TextButton(onPressed: () => Navigator.pop(context, false), child: const Text('Cancel')),
                        FilledButton(onPressed: () => Navigator.pop(context, true), child: const Text('Delete')),
                      ],
                    ),
                  ) ??
                  false;
            },
            onDismissed: (_) => _delete(c),
            child: ListTile(
              leading: CircleAvatar(child: Text(c.display.isNotEmpty ? c.display[0].toUpperCase() : '?')),
              title: Text(c.display),
              subtitle: c.name != null ? Text(c.email) : null,
              trailing: c.status != null && c.status != 'subscribed'
                  ? Chip(label: Text(c.status!), visualDensity: VisualDensity.compact)
                  : null,
            ),
          );
        },
      ),
    );
  }
}

class _NewContact {
  _NewContact(this.email, this.name);
  final String email;
  final String? name;
}

class _AddContactSheet extends StatefulWidget {
  const _AddContactSheet();
  @override
  State<_AddContactSheet> createState() => _AddContactSheetState();
}

class _AddContactSheetState extends State<_AddContactSheet> {
  final _email = TextEditingController();
  final _name = TextEditingController();

  @override
  void dispose() {
    _email.dispose();
    _name.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: EdgeInsets.fromLTRB(16, 16, 16, MediaQuery.of(context).viewInsets.bottom + 16),
      child: Column(mainAxisSize: MainAxisSize.min, children: [
        Text('Add contact', style: Theme.of(context).textTheme.titleMedium),
        const SizedBox(height: 12),
        TextField(controller: _email, decoration: const InputDecoration(labelText: 'Email', prefixIcon: Icon(Icons.alternate_email)), keyboardType: TextInputType.emailAddress),
        const SizedBox(height: 8),
        TextField(controller: _name, decoration: const InputDecoration(labelText: 'Name (optional)', prefixIcon: Icon(Icons.person_outline))),
        const SizedBox(height: 16),
        FilledButton(
          onPressed: () {
            final e = _email.text.trim();
            if (!e.contains('@')) return;
            Navigator.pop(context, _NewContact(e, _name.text.trim().isEmpty ? null : _name.text.trim()));
          },
          child: const Text('Add'),
        ),
      ]),
    );
  }
}
