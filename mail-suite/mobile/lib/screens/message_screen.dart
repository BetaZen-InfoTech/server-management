// MessageScreen — single-message detail. Loads the full body on push;
// shows a skeleton header (subject + from + date) immediately so the
// operator never sees a blank screen waiting on the network.

import 'package:flutter/material.dart';
import 'package:flutter_html/flutter_html.dart';
import 'package:intl/intl.dart';
import 'package:provider/provider.dart';

import '../models/message.dart';
import '../services/api_client.dart';
import '../services/mail_service.dart';

class MessageScreen extends StatefulWidget {
  const MessageScreen({super.key, required this.messageId});
  static const route = '/message';

  final String messageId;

  @override
  State<MessageScreen> createState() => _MessageScreenState();
}

class _MessageScreenState extends State<MessageScreen> {
  Message? _message;
  bool _loading = true;
  String? _error;

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
      final m = await context.read<MailService>().getMessage(widget.messageId);
      setState(() => _message = m);
    } on ApiException catch (e) {
      setState(() => _error = e.message);
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _toggleStar() async {
    final m = _message;
    if (m == null) return;
    final next = !m.isStarred;
    setState(() {
      _message = Message(
        id: m.id,
        subject: m.subject,
        from: m.from,
        to: m.to,
        cc: m.cc,
        bcc: m.bcc,
        date: m.date,
        isRead: m.isRead,
        isStarred: next,
        hasAttachments: m.hasAttachments,
        snippet: m.snippet,
        bodyHtml: m.bodyHtml,
        bodyText: m.bodyText,
      );
    });
    try {
      await context.read<MailService>().setFlag(m.id, 'starred', next);
    } on ApiException catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Could not update: ${e.message}')),
      );
      // Revert on failure so the UI state doesn't drift from the
      // server.
      setState(() => _message = m);
    }
  }

  Future<void> _delete() async {
    final id = widget.messageId;
    try {
      await context.read<MailService>().deleteMessage(id);
      if (!mounted) return;
      Navigator.of(context).pop(true);
    } on ApiException catch (e) {
      if (!mounted) return;
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Delete failed: ${e.message}')),
      );
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('Message'),
        actions: [
          IconButton(
            icon: Icon(
              (_message?.isStarred ?? false) ? Icons.star : Icons.star_outline,
              color: (_message?.isStarred ?? false) ? Colors.amber : null,
            ),
            onPressed: _message == null ? null : _toggleStar,
          ),
          IconButton(
            icon: const Icon(Icons.delete_outline),
            onPressed: _loading ? null : _delete,
          ),
        ],
      ),
      body: _body(),
    );
  }

  Widget _body() {
    if (_loading && _message == null) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_error != null && _message == null) {
      return Center(
        child: Padding(
          padding: const EdgeInsets.all(24),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              const Icon(Icons.cloud_off_outlined, size: 48),
              const SizedBox(height: 8),
              Text(_error!, textAlign: TextAlign.center),
              const SizedBox(height: 12),
              FilledButton.tonal(onPressed: _load, child: const Text('Retry')),
            ],
          ),
        ),
      );
    }
    final m = _message!;
    final theme = Theme.of(context);
    return SingleChildScrollView(
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(m.subject, style: theme.textTheme.titleLarge),
          const SizedBox(height: 12),
          Row(
            children: [
              CircleAvatar(
                backgroundColor: theme.colorScheme.primaryContainer,
                child: Text(
                  (m.from.display.isNotEmpty ? m.from.display[0] : '?').toUpperCase(),
                  style: TextStyle(color: theme.colorScheme.onPrimaryContainer),
                ),
              ),
              const SizedBox(width: 12),
              Expanded(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(m.from.display, style: theme.textTheme.titleSmall),
                    Text(
                      m.from.email,
                      style: theme.textTheme.bodySmall?.copyWith(
                        color: theme.colorScheme.outline,
                      ),
                    ),
                  ],
                ),
              ),
              Text(
                DateFormat('MMM d, h:mm a').format(m.date),
                style: theme.textTheme.bodySmall,
              ),
            ],
          ),
          const SizedBox(height: 8),
          Text(
            'To: ${m.to.map((a) => a.display).join(', ')}',
            style: theme.textTheme.bodySmall,
          ),
          const Divider(height: 32),
          if (m.bodyHtml != null && m.bodyHtml!.isNotEmpty)
            Html(data: m.bodyHtml!)
          else
            SelectableText(m.bodyText ?? '(empty)'),
        ],
      ),
    );
  }
}
