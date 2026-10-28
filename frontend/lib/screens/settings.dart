import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/api_client.dart';
import '../data/models.dart';
import '../design/icons.dart';
import '../design/tokens.dart';
import '../design/typography.dart';
import '../l10n/strings.dart';
import '../state/providers.dart';
import '../widgets/controls.dart';
import '../widgets/format.dart';
import '../widgets/rows.dart';
import '../widgets/track_menu.dart';

class SettingsScreen extends ConsumerWidget {
  const SettingsScreen({super.key});

  Future<void> _run(WidgetRef ref, Future<void> Function() call) async {
    try {
      await call();
    } on ApiException catch (e) {
      ref.read(toastProvider.notifier).error(e);
    }
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final api = ref.read(apiProvider);
    final connected = ref.watch(connectedProvider);
    final lib = ref.watch(libraryStatusProvider);
    final roots = ref.watch(rootsProvider).value ?? const <LibraryRoot>[];
    final muted = AppText.bodySm.copyWith(color: AppColors.inkMuted);
    return ScreenScroll(
      children: [
        const ScreenHeader(S.settings),
        const SectionLabel(S.screenSection, first: true),
        SettingToggle(
          title: S.visualizer,
          subtitle: S.visualizerHint,
          value: ref.watch(visualizerProvider),
          onChanged: ref.read(visualizerProvider.notifier).set,
        ),
        SettingToggle(
          title: S.calm,
          subtitle: S.calmHint,
          value: ref.watch(calmProvider),
          onChanged: ref.read(calmProvider.notifier).set,
        ),
        const SectionLabel(S.librarySection),
        for (final r in roots)
          SizedBox(
            height: AppSize.rowH,
            child: Row(
              children: [
                const ArpegioIcon(
                  'library',
                  size: AppSize.iconSm,
                  color: AppColors.inkMuted,
                ),
                const SizedBox(width: AppSpace.s3),
                Expanded(
                  child: Text(
                    r.path,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: AppText.body,
                  ),
                ),
                ArIconButton(
                  'close',
                  size: AppSize.iconSm,
                  muted: true,
                  label: 'Quitar carpeta',
                  onTap: () => _run(ref, () => api.removeRoot(r.id)),
                ),
              ],
            ),
          ),
        Padding(
          padding: const EdgeInsets.only(top: AppSpace.s2),
          child: Row(
            children: [
              ArButton(
                S.addFolder,
                icon: 'plus',
                ghost: true,
                onTap: () async {
                  final path = await promptText(
                    context,
                    S.addFolder,
                    S.folderPath,
                    S.create,
                  );
                  if (path != null && path.trim().isNotEmpty) {
                    await _run(ref, () => api.addRoot(path.trim()));
                  }
                },
              ),
              const SizedBox(width: AppSpace.s2),
              ArButton(
                S.rescan,
                ghost: true,
                onTap: connected && !lib.scanning
                    ? () => _run(ref, api.scan)
                    : null,
              ),
              const SizedBox(width: AppSpace.s3),
              if (lib.scanning)
                Text(
                  S
                      .indexing(lib.done, lib.total)
                      .replaceFirst('/${lib.total}', '/${fmtCount(lib.total)}'),
                  style: AppText.monoStatus.copyWith(
                    color: AppColors.neonAmber,
                  ),
                ),
            ],
          ),
        ),
        const SectionLabel(S.serverSection),
        SizedBox(
          height: AppSize.rowH,
          child: Row(
            children: [
              ArpegioIcon(
                'server',
                color: connected ? AppColors.signalOk : AppColors.signalError,
              ),
              const SizedBox(width: AppSpace.s3),
              Expanded(
                child: Column(
                  mainAxisAlignment: MainAxisAlignment.center,
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(
                      'arpegiod · ${Uri.parse(api.baseUrl).authority}',
                      style: AppText.body,
                    ),
                    Text(
                      connected ? 'Conectado' : 'Sin conexión',
                      style: muted,
                    ),
                  ],
                ),
              ),
            ],
          ),
        ),
      ],
    );
  }
}
