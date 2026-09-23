import VerifiedOutlinedIcon from '@mui/icons-material/VerifiedOutlined';
import BlockOutlinedIcon from '@mui/icons-material/BlockOutlined';
import type { DomainRecord, EvidenceReviewSnapshot } from '../../types/domain';

// SnapshotPanel renders the frozen cold-chain evidence review snapshot (evidence code and the
// first 12 chars of its SHA-256) or, while an excursion is still pending review, the blocking
// reason and conflict reference returned by the review-snapshot gate.
export function SnapshotPanel({ excursion, snapshot }: { excursion: DomainRecord; snapshot?: EvidenceReviewSnapshot | null }) {
  const code = excursion.snapshotCode || snapshot?.code || '';
  const sha = snapshot?.sha256 || excursion.snapshotSha256 || '';
  const evidenceCode = snapshot?.evidenceCode || excursion.snapshotEvidenceCode || '';

  if (code && sha) {
    return <section className="snapshot-panel snapshot-panel--frozen">
      <header><span><VerifiedOutlinedIcon />冷链证据复核快照</span><strong>{code}</strong></header>
      <dl>
        <div><dt>证据编号</dt><dd>{evidenceCode || '-'}</dd></div>
        <div><dt>SHA-256（前 12 位）</dt><dd>{sha.slice(0, 12)}</dd></div>
        <div><dt>容器编码</dt><dd>{snapshot?.containerCode || excursion.containerCode || '-'}</dd></div>
      </dl>
    </section>;
  }

  if (excursion.status === 'in_review' && excursion.reviewBlockCode) {
    return <section className="snapshot-panel snapshot-panel--blocked" role="alert">
      <header><span><BlockOutlinedIcon />复核快照阻断</span><strong>{excursion.reviewBlockCode}</strong></header>
      <p>{excursion.reviewBlockReason || '证据复核未通过，偏差保持待复核。'}</p>
      {excursion.reviewConflictRef && <small>冲突编号：{excursion.reviewConflictRef}</small>}
    </section>;
  }

  return null;
}
