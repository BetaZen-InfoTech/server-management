// SignaturesScreen — list/add/delete HTML signatures.

import 'package:flutter/material.dart';
import 'package:flutter_html/flutter_html.dart';
import 'package:provider/provider.dart';

import '../../models/signature.dart';
import '../../services/api_client.dart';
import '../../services/signatures_service.dart';

class SignaturesScreen extends StatefulWidget {
  const SignaturesScreen({super.key});
  static const route = '/settings/signatures';

  @override
  State<SignaturesScreen> createState() => _SignaturesScreenState();
}

class _SignaturesScreenState extends State<SignaturesScreen> {
  List<Signature> _items = const [];
  bool _loading = true;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    setState(() => _loading = true);
    try {
      _items = await context.read<SignaturesService>().list();
    } on ApiException catch (e) {
      if (mounted) {
        ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(e.message)));
      }
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('HTML signatures')),
      floatingActionButton: FloatingActionButton.extended(
        icon: const Icon(Icons.add),
        label: const Text('Add'),
        onPressed: _addDialog,
      ),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : ListView.separated(
              padding: const EdgeInsets.all(8),
              itemCount: _items.length,
              separatorBuilder: (_, __) => const SizedBox(height: 8),
              itemBuilder: (_, i) {
                final s = _items[i];
                return Card(
                  child: Padding(
                    padding: const EdgeInsets.all(12),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Row(
                          children: [
                            Expanded(
                              child: Text(s.name,
                                  style: Theme.of(context).textTheme.titleMedium),
                            ),
                            if (s.isDefault) const Chip(label: Text('Default')),
                            IconButton(
                              icon: const Icon(Icons.delete_outline),
                              onPressed: () async {
                                try {
                                  await context.read<SignaturesService>().delete(s.id);
                                  await _load();
                                } on ApiException catch (e) {
                                  if (!mounted) return;
                                  ScaffoldMessenger.of(context).showSnackBar(
                                    SnackBar(content: Text(e.message)),
                                  );
                                }
                              },
                            ),
                          ],
                        ),
                        const SizedBox(height: 6),
                        Html(data: s.html),
                      ],
                    ),
                  ),
                );
              },
            ),
    );
  }

  Future<void> _addDialog() async {
    final formKey = GlobalKey<FormState>();
    final name = TextEditingController();
    final html = TextEditingController(text: '<p>Best regards,<br/>Your name</p>');
    bool isDefault = false;

    await showDialog<void>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('New signature'),
        content: StatefulBuilder(
          builder: (ctx, setStateDialog) => Form(
            key: formKey,
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextFormField(
                  controller: name,
                  decoration: const InputDecoration(labelText: 'Name'),
                  validator: (v) => (v ?? '').isEmpty ? 'Required' : null,
                ),
                TextFormField(
                  controller: html,
                  maxLines: 6,
                  decoration: const InputDecoration(labelText: 'HTML'),
                ),
                SwitchListTile(
                  title: const Text('Use as default'),
                  value: isDefault,
                  onChanged: (v) => setStateDialog(() => isDefault = v),
                ),
              ],
            ),
          ),
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx), child: const Text('Cancel')),
          FilledButton(
            onPressed: () async {
              if (!(formKey.currentState?.validate() ?? false)) return;
              try {
                await context.read<SignaturesService>().create(
                      name: name.text.trim(),
                      html: html.text,
                      isDefault: isDefault,
                    );
                if (ctx.mounted) Navigator.pop(ctx);
                await _load();
              } on ApiException catch (e) {
                if (!ctx.mounted) return;
                ScaffoldMessenger.of(ctx).showSnackBar(
                  SnackBar(content: Text(e.message)),
                );
              }
            },
            child: const Text('Save'),
          ),
        ],
      ),
    );
  }
}
