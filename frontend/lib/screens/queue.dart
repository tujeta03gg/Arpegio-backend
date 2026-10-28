import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../design/tokens.dart';
import '../l10n/strings.dart';
import '../state/providers.dart';
import '../widgets/controls.dart';
import '../widgets/rows.dart';
import '../widgets/track_menu.dart';

class QueueScreen extends ConsumerWidget {
  const QueueScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final q = ref.watch(queueProvider);
    final cur = q.current;
    final next = q.upcoming;
    final base = q.currentIndex + 1;
    final n = ref.read(queueProvider.notifier);
    return CustomScrollView(
      slivers: [
        SliverPadding(
          padding: const EdgeInsets.fromLTRB(
            AppSpace.s4,
            AppSpace.s4,
            AppSpace.s4,
            0,
          ),
          sliver: SliverList.list(
            children: [
              ScreenHeader(
                S.queue,
                trailing: next.isEmpty
                    ? null
                    : ArButton(
                        S.clearQueue,
                        ghost: true,
                        onTap: () async {
                          if (await confirmDialog(
                            context,
                            S.clearQueueAsk,
                            S.confirm,
                          )) {
                            n.clear();
                          }
                        },
                      ),
              ),
              const SectionLabel(S.nowPlayingSection, first: true),
              if (cur != null)
                TrackRow(
                  track: cur.track,
                  onTap: () => ref.read(navProvider.notifier).go(Dest.now),
                ),
              SectionLabel(S.upNext(next.length)),
              if (next.isEmpty) const EmptyNote(S.emptyQueue),
            ],
          ),
        ),
        SliverPadding(
          padding: const EdgeInsets.symmetric(horizontal: AppSpace.s4),
          sliver: SliverReorderableList(
            itemCount: next.length,
            onReorderItem: (from, to) {
              if (from != to) n.move(base + from, base + to);
            },
            itemBuilder: (_, i) {
              final it = next[i];
              return ReorderableDelayedDragStartListener(
                key: ValueKey(it.position),
                index: i,
                child: Dismissible(
                  key: ValueKey('d${it.position}'),
                  direction: DismissDirection.endToStart,
                  onDismissed: (_) => n.remove(it),
                  background: Container(color: AppColors.void200),
                  child: TrackRow(
                    track: it.track,
                    tag: it.priority == 1 ? S.nextTag : null,
                    onTap: () {},
                  ),
                ),
              );
            },
          ),
        ),
        const SliverToBoxAdapter(child: SizedBox(height: AppSpace.s4)),
      ],
    );
  }
}
