import { request } from './client';
import type { EvidenceReviewSnapshot } from '../types/domain';

export async function listEvidenceSnapshots(excursionCode = '') {
  const query = excursionCode ? `&excursionCode=${encodeURIComponent(excursionCode)}` : '';
  return request<EvidenceReviewSnapshot[]>(`/evidence-snapshots?page=1&pageSize=100${query}`);
}
