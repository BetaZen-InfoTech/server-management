// InboxScreen — folders in a left Drawer + message list as the body.
//
// Master-detail-on-phone is awkward; the operator on Android/iOS
// expects: tap a folder → list, tap a message → push detail screen.
// We do exactly that.
//
// The folder list lives in the Drawer so an iOS-thumb-reachable
// hamburger toggle exposes it without eating screen real estate on
// small phones. Tablet/landscape can grow into a permanent rail
// later — the model + screen state already supports it.

import 'package:flutter/material.dart';
import 'package:intl/intl.dart';
import 'package:provider/provider.dart';

import '../models/mailbox.dart';
import '../models/message.dart';
import '../services/api_client.dart';
import '../services/auth_service.dart';
import '../services/mail_service.dart';
import 'compose_screen.dart';
import 'message_screen.dart';
import 'settings/settings_screen.dart';

class InboxScreen extends StatefulWidget {
  const InboxScreen({super.key});
  static const route = '/inbox';

  @override
  State<InboxScreen> createState() => _InboxScreenState();
}

class _InboxScreenState extends State<InboxScreen> {
  List<Mailbox> _mailboxes = const [];
  Mailbox? _current;
  List<Message> _messages = const [];
  bool _loadingFolders = false;
  bool _loadingMessages = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _fetchFolders();
  }

  Future<void> _fetchFolders() async {
    setState(() {
      _loadingFolders = true;
      _error = null;
    });
    try {
      final mail = context.read<MailService>();
      final folders = await mail.listMailboxes();
      // Prefer the inbox-flagged folder as the initial selection; fall
      // back to the first entry. An empty server response leaves
      // _current null and the body renders an empty state.
      final initial = folders.firstWhere(
        (f) => f.isInbox,
        orElse: () => folders.isNotEmpty ? folders.first : Mailbox(
          name: 'INBOX',
          path: 'INBOX',
          unreadCount: 0,
          totalCount: 0,
          special: 'inbox',
        ),
      );
      setState(() {
        _mailboxes = folders;
        _current = folders.isEmpty ? null : initial;
      });
      if (folders.isNotEmpty) await _fetchMessages(initial);
    } on ApiException catch (e) {
      setState(() => _error = e.message);
    } finally {
      if (mounted) setState(() => _loadingFolders = false);
    }
  }

  Future<void> _fetchMessages(Mailbox m) async {
    setState(() {
      _loadingMessages = true;
      _current = m;
      _error = null;
    });
    try {
      final mail = context.read<MailService>();
      final msgs = await mail.listMessages(m.path);
      setState(() => _messages = msgs);
    } on ApiException catch (e) {
      setState(() => _error = e.message);
    } finally {
      if (mounted) setState(() => _loadingMessages = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final auth = context.watch<AuthService>();
    return Scaffold(
      appBar: AppBar(
        title: Text(_current?.name ?? 'Mail'),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh),
            onPressed: _current == null ? null : () => _fetchMessages(_current!),
          ),
          IconButton(
            icon: const Icon(Icons.settings_outlined),
            onPressed: () =>
                Navigator.of(context).pushNamed(SettingsScreen.route),
          ),
        ],
      ),
      drawer: _FoldersDrawer(
        account: auth.account?.email ?? '',
        loading: _loadingFolders,
        mailboxes: _mailboxes,
        current: _current,
        onSelect: (m) {
          Navigator.of(context).pop();
          _fetchMessages(m);
        },
      ),
      floatingActionButton: FloatingActionButton.extended(
        icon: const Icon(Icons.edit_outlined),
        label: const Text('Compose'),
        onPressed: () => Navigator.of(context).pushNamed(ComposeScreen.route),
      ),
      body: _body(),
    );
  }

  Widget _body() {
    if (_error != null && _messages.isEmpty) {
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
              FilledButton.tonal(
                onPressed: _fetchFolders,
                child: const Text('Retry'),
              ),
            ],
          ),
        ),
      );
    }
    if (_loadingMessages && _messages.isEmpty) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_messages.isEmpty) {
      return const Center(child: Text('No messages in this folder'));
    }
    return RefreshIndicator(
      onRefresh: () =>
          _current == null ? Future<void>.value() : _fetchMessages(_current!),
      child: ListView.separated(
        itemCount: _messages.length,
        separatorBuilder: (_, __) => const Divider(height: 1),
        itemBuilder: (_, i) => _MessageRow(message: _messages[i]),
      ),
    );
  }
}

class _FoldersDrawer extends StatelessWidget {
  const _FoldersDrawer({
    required this.account,
    required this.loading,
    required this.mailboxes,
    required this.current,
    required this.onSelect,
  });

  final String account;
  final bool loading;
  final List<Mailbox> mailboxes;
  final Mailbox? current;
  final void Function(Mailbox) onSelect;

  @override
  Widget build(BuildContext context) {
    return Drawer(
      child: SafeArea(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            DrawerHeader(
              decoration: BoxDecoration(color: Theme.of(context).colorScheme.primaryContainer),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                mainAxisAlignment: MainAxisAlignment.end,
                children: [
                  const Icon(Icons.mail_outline, size: 32),
                  const SizedBox(height: 8),
                  Text(account, style: Theme.of(context).textTheme.titleMedium),
                ],
              ),
            ),
            if (loading)
              const Padding(
                padding: EdgeInsets.all(16),
                child: LinearProgressIndicator(),
              ),
            Expanded(
              child: ListView(
                children: [
                  for (final m in mailboxes)
                    ListTile(
                      leading: Icon(_iconFor(m)),
                      title: Text(m.name),
                      trailing: m.unreadCount > 0
                          ? _unreadBadge(context, m.unreadCount)
                          : null,
                      selected: current?.path == m.path,
                      onTap: () => onSelect(m),
                    ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  IconData _iconFor(Mailbox m) {
    switch (m.special) {
      case 'inbox':
        return Icons.inbox_outlined;
      case 'sent':
        return Icons.send_outlined;
      case 'drafts':
        return Icons.drafts_outlined;
      case 'trash':
        return Icons.delete_outline;
      case 'spam':
        return Icons.report_outlined;
      case 'archive':
        return Icons.archive_outlined;
      default:
        return Icons.folder_outlined;
    }
  }

  Widget _unreadBadge(BuildContext context, int n) => Container(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
        decoration: BoxDecoration(
          color: Theme.of(context).colorScheme.primary,
          borderRadius: BorderRadius.circular(10),
        ),
        child: Text(
          '$n',
          style: TextStyle(
            color: Theme.of(context).colorScheme.onPrimary,
            fontSize: 11,
            fontWeight: FontWeight.w600,
          ),
        ),
      );
}

class _MessageRow extends StatelessWidget {
  const _MessageRow({required this.message});
  final Message message;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final weight = message.isRead ? FontWeight.w400 : FontWeight.w700;
    return ListTile(
      leading: CircleAvatar(
        backgroundColor: theme.colorScheme.primaryContainer,
        child: Text(
          (message.from.display.isNotEmpty
                  ? message.from.display[0]
                  : '?')
              .toUpperCase(),
          style: TextStyle(color: theme.colorScheme.onPrimaryContainer),
        ),
      ),
      title: Row(
        children: [
          Expanded(
            child: Text(
              message.from.display,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(fontWeight: weight),
            ),
          ),
          if (message.isStarred)
            const Icon(Icons.star, size: 14, color: Colors.amber),
          if (message.hasAttachments) ...[
            const SizedBox(width: 4),
            const Icon(Icons.attach_file, size: 14),
          ],
          const SizedBox(width: 6),
          Text(_relative(message.date), style: theme.textTheme.bodySmall),
        ],
      ),
      subtitle: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            message.subject,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: TextStyle(fontWeight: weight),
          ),
          if (message.snippet.isNotEmpty)
            Text(
              message.snippet,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: theme.textTheme.bodySmall,
            ),
        ],
      ),
      onTap: () => Navigator.of(context).pushNamed(
        MessageScreen.route,
        arguments: message.id,
      ),
    );
  }

  String _relative(DateTime d) {
    final now = DateTime.now();
    final today = DateTime(now.year, now.month, now.day);
    final that = DateTime(d.year, d.month, d.day);
    if (that == today) return DateFormat.jm().format(d);
    if (today.difference(that).inDays == 1) return 'Yesterday';
    if (now.year == d.year) return DateFormat('MMM d').format(d);
    return DateFormat('MMM d, y').format(d);
  }
}
