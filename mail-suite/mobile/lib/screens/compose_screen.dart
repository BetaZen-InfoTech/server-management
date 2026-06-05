// ComposeScreen — minimal new-message form. Send routes through
// MailService.sendMessage which posts to /api/v1/messages.
//
// Attachments are intentionally NOT in this first cut — they need a
// pickFile flow + multipart upload that's a separate piece of work.
// Added as a TODO note so the future PR has an explicit handle.

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../services/api_client.dart';
import '../services/mail_service.dart';

class ComposeScreen extends StatefulWidget {
  const ComposeScreen({super.key});
  static const route = '/compose';

  @override
  State<ComposeScreen> createState() => _ComposeScreenState();
}

class _ComposeScreenState extends State<ComposeScreen> {
  final _toCtrl = TextEditingController();
  final _ccCtrl = TextEditingController();
  final _subjectCtrl = TextEditingController();
  final _bodyCtrl = TextEditingController();
  bool _sending = false;
  String? _error;

  @override
  void dispose() {
    _toCtrl.dispose();
    _ccCtrl.dispose();
    _subjectCtrl.dispose();
    _bodyCtrl.dispose();
    super.dispose();
  }

  List<String> _parseAddresses(String raw) => raw
      .split(RegExp(r'[,;\s]+'))
      .map((s) => s.trim())
      .where((s) => s.isNotEmpty)
      .toList();

  Future<void> _send() async {
    final to = _parseAddresses(_toCtrl.text);
    if (to.isEmpty) {
      setState(() => _error = 'At least one To address is required');
      return;
    }
    setState(() {
      _sending = true;
      _error = null;
    });
    try {
      await context.read<MailService>().sendMessage(
            to: to,
            cc: _parseAddresses(_ccCtrl.text),
            subject: _subjectCtrl.text.trim(),
            body: _bodyCtrl.text,
          );
      if (!mounted) return;
      Navigator.of(context).pop();
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Message sent')),
      );
    } on ApiException catch (e) {
      setState(() => _error = e.message);
    } finally {
      if (mounted) setState(() => _sending = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('New message'),
        actions: [
          IconButton(
            icon: const Icon(Icons.send),
            tooltip: 'Send',
            onPressed: _sending ? null : _send,
          ),
        ],
      ),
      body: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          children: [
            TextField(
              controller: _toCtrl,
              decoration: const InputDecoration(
                labelText: 'To',
                hintText: 'name@example.com, second@example.com',
              ),
              keyboardType: TextInputType.emailAddress,
              autocorrect: false,
            ),
            TextField(
              controller: _ccCtrl,
              decoration: const InputDecoration(labelText: 'Cc'),
              keyboardType: TextInputType.emailAddress,
              autocorrect: false,
            ),
            TextField(
              controller: _subjectCtrl,
              decoration: const InputDecoration(labelText: 'Subject'),
            ),
            const SizedBox(height: 12),
            Expanded(
              child: TextField(
                controller: _bodyCtrl,
                decoration: const InputDecoration(
                  hintText: 'Write your message…',
                  border: OutlineInputBorder(),
                  alignLabelWithHint: true,
                ),
                maxLines: null,
                expands: true,
                textAlignVertical: TextAlignVertical.top,
                keyboardType: TextInputType.multiline,
              ),
            ),
            if (_error != null) ...[
              const SizedBox(height: 8),
              Text(_error!, style: TextStyle(color: Theme.of(context).colorScheme.error)),
            ],
            if (_sending) const LinearProgressIndicator(),
          ],
        ),
      ),
    );
  }
}
