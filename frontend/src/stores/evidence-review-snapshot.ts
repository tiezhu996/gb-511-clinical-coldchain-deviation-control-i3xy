import { create } from 'zustand';
import { listEvidenceSnapshots } from '../api/evidence-review-snapshot';
import type { EvidenceReviewSnapshot } from '../types/domain';

export const useEvidenceSnapshotStore = create<{
  items: EvidenceReviewSnapshot[];
  error: string;
  load: (excursionCode?: string) => Promise<void>;
}>((set) => ({
  items: [], error: '',
  load: async (excursionCode = '') => {
    try { set({ items: (await listEvidenceSnapshots(excursionCode)).data, error: '' }); }
    catch (error) { set({ error: error instanceof Error ? error.message : String(error) }); }
  },
}));
