// CampaignsScreen — mobile view of email campaigns: list with live
// status + send progress, tap for open/click analytics, and pause/
// resume/cancel controls. Brings the webmail's Campaigns feature to app.

import 'package:flutter/material.dart';
import 'package:provider/provider.dart';

import '../services/api_client.dart';
import '../services/campaign_service.dart';

class CampaignsScreen extends StatefulWidget {
  const CampaignsScreen({super.key});
  static const route = '/campaigns';

  @override
  State<CampaignsScreen> createState() => _CampaignsScreenState();
}

class _CampaignsScreenState extends State<CampaignsScreen> {
  List<Campaign> _campaigns = const [];
  bool _loading = false;
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
      final list = await context.read<CampaignService>().list();
      if (mounted) setState(() => _campaigns = list);
    } on ApiException catch (e) {
      if (mounted) setState(() => _error = e.message);
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  Future<void> _act(Campaign c, String action) async {
    final svc = context.read<CampaignService>();
    try {
      switch (action) {
        case 'pause':
          await svc.pause(c.id);
        case 'resume':
          await svc.resume(c.id);
        case 'cancel':
          await svc.cancel(c.id);
      }
      await _load();
    } on ApiException catch (e) {
      if (mounted) ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(e.message)));
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('Campaigns'), actions: [
        IconButton(icon: const Icon(Icons.refresh), onPressed: _load),
      ]),
      body: _body(),
    );
  }

  Widget _body() {
    if (_error != null && _campaigns.isEmpty) {
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
    if (_loading && _campaigns.isEmpty) {
      return const Center(child: CircularProgressIndicator());
    }
    if (_campaigns.isEmpty) {
      return const Center(child: Text('No campaigns yet.\nCreate one on the webmail.', textAlign: TextAlign.center));
    }
    return RefreshIndicator(
      onRefresh: _load,
      child: ListView.separated(
        padding: const EdgeInsets.all(12),
        itemCount: _campaigns.length,
        separatorBuilder: (_, __) => const SizedBox(height: 10),
        itemBuilder: (_, i) => _CampaignCard(campaign: _campaigns[i], onAction: _act),
      ),
    );
  }
}

class _CampaignCard extends StatelessWidget {
  const _CampaignCard({required this.campaign, required this.onAction});
  final Campaign campaign;
  final void Function(Campaign, String) onAction;

  @override
  Widget build(BuildContext context) {
    final c = campaign;
    final showProgress = c.status == 'sending' || c.status == 'sent' || c.status == 'paused';
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(14),
        child: Column(crossAxisAlignment: CrossAxisAlignment.start, children: [
          Row(children: [
            Expanded(child: Text(c.name, style: Theme.of(context).textTheme.titleMedium, overflow: TextOverflow.ellipsis)),
            _statusChip(context, c.status),
            if (c.mode == 'drip') ...[
              const SizedBox(width: 6),
              const Icon(Icons.schedule, size: 16),
            ],
          ]),
          if (c.subject.isNotEmpty) ...[
            const SizedBox(height: 2),
            Text(c.subject, style: Theme.of(context).textTheme.bodySmall, maxLines: 1, overflow: TextOverflow.ellipsis),
          ],
          if (showProgress) ...[
            const SizedBox(height: 10),
            Row(children: [
              Expanded(child: LinearProgressIndicator(value: c.total > 0 ? c.sent / c.total : 0, minHeight: 6)),
              const SizedBox(width: 8),
              Text('${c.sent}/${c.total} · ${c.pct}%', style: Theme.of(context).textTheme.bodySmall),
            ]),
          ],
          const SizedBox(height: 6),
          Row(children: [
            TextButton.icon(
              icon: const Icon(Icons.bar_chart, size: 18),
              label: const Text('Stats'),
              onPressed: () => _showStats(context, c),
            ),
            const Spacer(),
            if (c.status == 'sending')
              TextButton(onPressed: () => onAction(c, 'pause'), child: const Text('Pause')),
            if (c.status == 'paused')
              TextButton(onPressed: () => onAction(c, 'resume'), child: const Text('Resume')),
            if (c.status == 'sending' || c.status == 'paused')
              TextButton(onPressed: () => onAction(c, 'cancel'), child: const Text('Cancel')),
          ]),
        ]),
      ),
    );
  }

  Widget _statusChip(BuildContext context, String status) {
    Color bg;
    switch (status) {
      case 'sent':
        bg = Colors.green;
      case 'sending':
        bg = Colors.blue;
      case 'paused':
        bg = Colors.amber;
      case 'failed':
        bg = Colors.red;
      case 'canceled':
        bg = Colors.grey;
      default:
        bg = Colors.blueGrey;
    }
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(color: bg.withValues(alpha: 0.18), borderRadius: BorderRadius.circular(10)),
      child: Text(status, style: TextStyle(color: bg, fontSize: 12, fontWeight: FontWeight.w600)),
    );
  }

  Future<void> _showStats(BuildContext context, Campaign c) async {
    showModalBottomSheet<void>(
      context: context,
      builder: (ctx) => FutureBuilder<CampaignStats>(
        future: ctx.read<CampaignService>().stats(c.id),
        builder: (ctx, snap) {
          if (!snap.hasData) {
            return const SizedBox(height: 180, child: Center(child: CircularProgressIndicator()));
          }
          final s = snap.data!;
          return Padding(
            padding: const EdgeInsets.all(20),
            child: Column(mainAxisSize: MainAxisSize.min, crossAxisAlignment: CrossAxisAlignment.start, children: [
              Text(c.name, style: Theme.of(ctx).textTheme.titleLarge),
              const SizedBox(height: 16),
              Row(mainAxisAlignment: MainAxisAlignment.spaceAround, children: [
                _stat(ctx, 'Sent', s.sent),
                _stat(ctx, 'Delivered', s.delivered),
                _stat(ctx, 'Opened', s.opened),
                _stat(ctx, 'Clicked', s.clicked),
              ]),
            ]),
          );
        },
      ),
    );
  }

  Widget _stat(BuildContext ctx, String label, int n) => Column(children: [
        Text('$n', style: Theme.of(ctx).textTheme.headlineSmall),
        Text(label, style: Theme.of(ctx).textTheme.bodySmall),
      ]);
}
