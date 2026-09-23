
import { useEffect, useMemo, useState } from 'react';
import Button from '@mui/material/Button';
import AddOutlinedIcon from '@mui/icons-material/AddOutlined';
import VerifiedUserOutlinedIcon from '@mui/icons-material/VerifiedUserOutlined';
import BlockOutlinedIcon from '@mui/icons-material/BlockOutlined';
import DeleteSweepOutlinedIcon from '@mui/icons-material/DeleteSweepOutlined';
import { useDispositionDecisionStore } from '../stores/disposition-decision';
import { useExcursionEventStore } from '../stores/excursion-event';
import type { DomainRecord } from '../types/domain';
import { roleAtLeast } from '../types/domain';
import { getSession } from '../api/client';
import { usePolling } from '../hooks/usePolling';
import { MetricCard } from '../components/common/MetricCard';
import { StatusBadge } from '../components/common/StatusBadge';
import { DecisionPanel } from '../components/common/DecisionPanel';
import { ConfirmDialog } from '../components/common/ConfirmDialog';

export default function DispositionDecisionPage() {
  const store = useDispositionDecisionStore();
  const excursions = useExcursionEventStore();
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [target, setTarget] = useState('');
  const session = getSession();
  const canPropose = roleAtLeast(session?.role, 'operator');
  const canApprove = roleAtLeast(session?.role, 'reviewer');
  useEffect(() => { void store.load('dispositions'); void excursions.load('excursions'); }, [store.load, excursions.load]);
  usePolling(() => store.load('dispositions'), 15000);
  useEffect(() => { if (selectedId === null && store.items[0]) setSelectedId(store.items[0].id); }, [store.items, selectedId]);
  const selected = store.items.find((item) => item.id === selectedId) || null;
  const drafts = useMemo(() => store.items.filter((item) => item.status === 'draft').length, [store.items]);
  const released = useMemo(() => store.items.filter((item) => item.status === 'release').length, [store.items]);
  const createProposal = async () => {
    const suffix = Date.now().toString().slice(-5); const now = new Date().toISOString();
    const decided = excursions.items.find((item) => item.status === 'decided');
    const excursionCode = decided?.code || 'EE-003';
    const evidenceRef = decided?.sensorEvidence || decided?.evidence || 'minio://sensor/tc-001/door-open.csv';
    await store.createRecord('dispositions', { code: `DD-UI-${suffix}`, name: `${excursionCode} 隔离处置提议`, description: '偏差影响评估完成，依据证据复核快照提议处置', facility: '质量放行组', owner: session?.username || 'operator', category: '隔离提议', riskLevel: 'high', metricValue: decided?.observedTempC ?? decided?.metricValue ?? 0, metricUnit: decided?.metricUnit || 'C', effectiveAt: now, evidence: evidenceRef, relatedCode: excursionCode, excursionCode, decisionBasis: '依据证据复核快照完成影响评估，等待独立复核', sensorEvidence: evidenceRef });
    setCreateOpen(false);
  };
  const approve = async () => {
    if (!selected || !target) return;
    const reason = target === 'release' ? '稳定性评估支持临床使用，批准放行' : target === 'discard' ? '偏差影响不可接受，批准报废' : '证据尚不足，维持物理隔离';
    await store.transition('dispositions', selected, target, reason, selected.sensorEvidence || selected.evidence);
    setTarget('');
  };
  return <main className="workspace"><header className="page-header"><div><p className="eyebrow">QUALITY DISPOSITION</p><h1>处置决定审核</h1><p>放行、隔离与报废均要求传感器证据，并由不同于提议人的质量角色复核。</p></div>{canPropose && <Button variant="contained" startIcon={<AddOutlinedIcon />} onClick={() => setCreateOpen(true)}>新建处置提议</Button>}</header>
    <section className="metrics"><MetricCard label="处置记录" value={store.meta.total} detail="全流程留痕" /><MetricCard label="待复核" value={drafts} detail="需要第二人" /><MetricCard label="已放行" value={released} detail="证据评估通过" /></section>
    {store.error && <div className="alert" role="alert">{store.error}</div>}
    <section className="split-workspace"><div className="record-list">{store.items.map((item) => <button key={item.id} className={selectedId === item.id ? 'record-row selected' : 'record-row'} onClick={() => setSelectedId(item.id)}><span><strong>{item.code}</strong><small>{item.excursionCode || item.relatedCode} · 提议人 {item.proposedBy || item.owner}</small></span><StatusBadge status={item.status} /></button>)}</div><aside className="detail-pane">{selected ? <><DecisionPanel decision={selected} />{canApprove && selected.status === 'draft' && <div className="decision-actions"><Button startIcon={<VerifiedUserOutlinedIcon />} onClick={() => setTarget('release')}>放行</Button><Button color="warning" startIcon={<BlockOutlinedIcon />} onClick={() => setTarget('quarantine')}>隔离</Button><Button color="error" startIcon={<DeleteSweepOutlinedIcon />} onClick={() => setTarget('discard')}>报废</Button></div>}<div className="dual-control"><strong>双人复核</strong><span>提议：{selected.proposedBy || '-'}</span><span>批准：{selected.approvedBy || '待不同账号复核'}</span></div></> : <div className="empty">选择一条处置记录</div>}</aside></section>
    <ConfirmDialog open={createOpen} title="创建隔离处置提议" onCancel={() => setCreateOpen(false)} onConfirm={() => void createProposal()}><p>提议只能针对已完成证据复核快照的已评估偏差；系统自动记录当前登录人为提议人，提议人不能批准自己的处置。</p></ConfirmDialog>
    <ConfirmDialog open={Boolean(target)} title={`确认${target === 'release' ? '放行' : target === 'discard' ? '报废' : '隔离'}决定`} onCancel={() => setTarget('')} onConfirm={() => void approve()}><p>批准时系统会再次校验偏差的证据复核快照摘要；最终决定保存提议人、独立复核人、传感器证据和 request ID。</p><strong>{selected?.code} · {selected?.excursionCode}</strong></ConfirmDialog>
  </main>;
}
