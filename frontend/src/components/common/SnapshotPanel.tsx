
import FactCheckOutlinedIcon from '@mui/icons-material/FactCheckOutlined';
import type { DomainRecord } from '../../types/domain';

// SnapshotPanel shows the frozen evidence review snapshot (code, digest prefix and
// container) plus the persisted blocking reason while an excursion stays in review.
export function SnapshotPanel({ excursion }: { excursion: DomainRecord }) {
  if (!excursion.reviewSnapshotCode && !excursion.reviewBlockReason) return null;
  const digestPrefix = (excursion.reviewSnapshotDigest || '').slice(0, 12);
  return <section className="snapshot-panel">
    <header><span><FactCheckOutlinedIcon />证据复核快照</span></header>
    {excursion.reviewSnapshotCode ? <dl>
      <div><dt>快照编号</dt><dd>{excursion.reviewSnapshotCode}</dd></div>
      <div><dt>摘要前12位</dt><dd>{digestPrefix}…</dd></div>
      <div><dt>冻结容器</dt><dd>{excursion.reviewSnapshotContainer || '-'}</dd></div>
    </dl> : <p className="muted">完成影响评估时自动冻结证据编号、SHA-256 与容器编码。</p>}
    {excursion.reviewBlockReason && <p className="snapshot-block" role="alert">阻断原因：{excursion.reviewBlockReason}</p>}
  </section>;
}
