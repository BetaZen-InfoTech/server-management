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

import '../models/mail_account.dart';
import '../models/mailbox.dart';
import '../models/message.dart';
import '../services/account_service.dart';
import '../services/api_client.dart';
import '../services/mail_service.dart';
import 'campaigns_screen.dart';
import 'compose_screen.dart';
import 'contacts_screen.dart';
import 'message_screen.dart';
import 'settings/mail_accounts_screen.dart';
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
  AccountService? _accountsSvc;
  String? _fetchedAccountId; // the account whose folders are currently shown

  @override
  void initState() {
    super.initState();
    // Defer to after the first frame so the Provider is available, then make
    // sure accounts are loaded before fetching folders. Cold-start fires
    // AccountService.load() fire-and-forget, so without this the inbox can
    // render before any account exists → a spurious "No mail account selected".
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted) return;
      _accountsSvc = context.read<AccountService>();
      _accountsSvc!.addListener(_onAccountsChanged);
      _ensureAccountsAndFetch();
    });
  }

  @override
  void dispose() {
    _accountsSvc?.removeListener(_onAccountsChanged);
    super.dispose();
  }

  // Re-fetch folders when the selected account first appears (background load
  // finishing) or changes, so the inbox self-heals instead of stranding on the
  // empty state.
  void _onAccountsChanged() {
    if (!mounted) return;
    final sel = _accountsSvc?.selected;
    if (sel != null && sel.id != _fetchedAccountId) {
      _fetchedAccountId = sel.id;
      _fetchFolders();
    }
  }

  // Loads the account list if it isn't loaded yet, then fetches folders. Used by
  // first render AND the Retry button — so a failed cold-start account load can
  // actually recover (Retry previously only re-fetched folders, never accounts).
  Future<void> _ensureAccountsAndFetch() async {
    final accounts = context.read<AccountService>();
    // Already have an account → just (re)fetch its folders (Retry / normal).
    if (accounts.selected != null) {
      _fetchedAccountId = accounts.selected!.id;
      await _fetchFolders();
      return;
    }
    // A load is already in flight (cold-start fire-and-forget) → show a spinner,
    // NOT the empty error; _onAccountsChanged fetches when it lands.
    if (accounts.loading) {
      setState(() {
        _loadingFolders = true;
        _error = null;
      });
      return;
    }
    // Nothing loaded and nothing loading → trigger a load ourselves.
    setState(() {
      _loadingFolders = true;
      _error = null;
    });
    try {
      await accounts.load();
    } catch (_) {/* surfaced below */}
    if (!mounted) return;
    final sel = accounts.selected;
    if (sel == null) {
      setState(() {
        _loadingFolders = false;
        _error = 'No mail account selected. Add one in Settings → Accounts.';
      });
    } else if (_fetchedAccountId != sel.id) {
      // Guard: if _onAccountsChanged already fetched during load()'s notify,
      // _fetchedAccountId is set and we skip the duplicate fetch.
      _fetchedAccountId = sel.id;
      await _fetchFolders();
    }
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

  // Switch the active mail account, then reload its folders + messages. The
  // account list + selection live in AccountService (shared with the webmail
  // contract), so this is the mobile equivalent of the webmail's top-right
  // account switcher.
  Future<void> _switchAccount(String id) async {
    _fetchedAccountId = id; // claim it so _onAccountsChanged doesn't double-fetch
    await context.read<AccountService>().select(id);
    if (mounted) await _fetchFolders();
  }

  // Virtual "Starred" folder (INBOX \Flagged on the backend) — most IMAP
  // servers have no real Starred mailbox, so it's not in /folders.
  static final _starred = Mailbox(name: 'Starred', path: 'Starred', unreadCount: 0, totalCount: 0, special: 'starred');

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
    final accounts = context.watch<AccountService>();
    // Prepend the virtual Starred folder to the real server folders.
    final folders = <Mailbox>[
      ..._mailboxes.take(1), // INBOX-ish first entry
      _starred,
      ..._mailboxes.skip(1),
    ];
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
        accounts: accounts.accounts,
        selectedAccountId: accounts.selected?.id,
        loading: _loadingFolders,
        mailboxes: _mailboxes.isEmpty ? const [] : folders,
        current: _current,
        onSelect: (m) {
          Navigator.of(context).pop();
          _fetchMessages(m);
        },
        onSwitchAccount: (id) {
          Navigator.of(context).pop();
          _switchAccount(id);
        },
        onAddAccount: () {
          Navigator.of(context).pop();
          Navigator.of(context).pushNamed(MailAccountsScreen.route).then((_) {
            context.read<AccountService>().load();
          });
        },
        onOpenContacts: () {
          Navigator.of(context).pop();
          Navigator.of(context).pushNamed(ContactsScreen.route);
        },
        onOpenCampaigns: () {
          Navigator.of(context).pop();
          Navigator.of(context).pushNamed(CampaignsScreen.route);
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
                onPressed: _ensureAccountsAndFetch,
                child: const Text('Retry'),
              ),
            ],
          ),
        ),
      );
    }
    if ((_loadingMessages || _loadingFolders) && _messages.isEmpty) {
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
    required this.accounts,
    required this.selectedAccountId,
    required this.loading,
    required this.mailboxes,
    required this.current,
    required this.onSelect,
    required this.onSwitchAccount,
    required this.onAddAccount,
    required this.onOpenContacts,
    required this.onOpenCampaigns,
  });

  final List<MailAccount> accounts;
  final String? selectedAccountId;
  final bool loading;
  final List<Mailbox> mailboxes;
  final Mailbox? current;
  final void Function(Mailbox) onSelect;
  final void Function(String) onSwitchAccount;
  final VoidCallback onAddAccount;
  final VoidCallback onOpenContacts;
  final VoidCallback onOpenCampaigns;

  @override
  Widget build(BuildContext context) {
    MailAccount? selected;
    for (final a in accounts) {
      if (a.id == selectedAccountId) {
        selected = a;
        break;
      }
    }
    selected ??= accounts.isNotEmpty ? accounts.first : null;
    final cs = Theme.of(context).colorScheme;
    return Drawer(
      child: SafeArea(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            // Account switcher — the mobile equivalent of the webmail's
            // top-right account dropdown. Expands to the full account list.
            Container(
              color: cs.primaryContainer,
              child: Theme(
                data: Theme.of(context).copyWith(dividerColor: Colors.transparent),
                child: ExpansionTile(
                  leading: CircleAvatar(
                    child: Text(selected != null && selected.short.isNotEmpty
                        ? selected.short[0].toUpperCase()
                        : '?'),
                  ),
                  title: Text(selected?.address ?? 'No account', overflow: TextOverflow.ellipsis),
                  subtitle: accounts.length > 1
                      ? Text('${accounts.length} accounts — tap to switch')
                      : const Text('Betazen Mail'),
                  childrenPadding: EdgeInsets.zero,
                  children: [
                    for (final a in accounts)
                      ListTile(
                        dense: true,
                        leading: Icon(
                          a.id == (selected?.id) ? Icons.check_circle : Icons.circle_outlined,
                          size: 20,
                        ),
                        title: Text(a.address, overflow: TextOverflow.ellipsis),
                        subtitle: Text('${a.provider}${a.isPrimary ? ' · primary' : ''}'),
                        onTap: () => onSwitchAccount(a.id),
                      ),
                    ListTile(
                      dense: true,
                      leading: const Icon(Icons.add, size: 20),
                      title: const Text('Add account'),
                      onTap: onAddAccount,
                    ),
                  ],
                ),
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
                  const Divider(),
                  ListTile(
                    leading: const Icon(Icons.people_outline),
                    title: const Text('Contacts'),
                    onTap: onOpenContacts,
                  ),
                  ListTile(
                    leading: const Icon(Icons.campaign_outlined),
                    title: const Text('Campaigns'),
                    onTap: onOpenCampaigns,
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
      case 'starred':
        return Icons.star_outline;
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
