import { validateContract } from '../../generated/validate-contract.mjs';
import type { GuideMessage, GuideRequirements, GuideResult } from '../../generated/contracts';

export const SESSION_KEY = 'yuanfa.guide.v1';
const resultSchema = 'guide.schema.json#/$defs/GuideResult';
const requirementsSchema = 'guide.schema.json#/$defs/GuideRequirements';
export const unknownRequirements = (): GuideRequirements => ({ scene_ids: [], material: null, substrate: null, nail_type: null, fastener_length_mm: null, air_supply: null, load_bearing: null, long_nose: null, blower: null });
export const checkedResult = (value: unknown): GuideResult => validateContract(resultSchema, structuredClone(value));
export function plainHistory(messages: GuideMessage[]): GuideMessage[] {
  const out: GuideMessage[] = [];
  let remaining = 16000;
  for (const message of messages.slice(-12).reverse()) {
    const content = [...message.content].slice(-remaining).join('');
    if (content) out.unshift({ role: message.role, content });
    remaining -= [...content].length;
    if (!remaining) break;
  }
  return out;
}
export type PendingTurn = { request_id: string; kind: 'consult' | 'select'; text: string; selected_product_id: string | null; correcting: true };
export type GuideState = { version: 1; messages: GuideMessage[]; result: GuideResult | null; requirements: GuideRequirements; release_id: string; pending_turn: PendingTurn | null; busy: boolean; checked: boolean; stale: boolean; storageLimited: boolean; notice: string };
type StoragePort = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;
export type Ticket = { id: string; epoch: number };
const keysOnly = (value: object, keys: string[]) => Object.keys(value).every(key => keys.includes(key));

/** One transaction boundary for button JSON, complete SSE and session restore. */
export class GuideSession {
  state: GuideState;
  private listeners = new Set<() => void>();
  private storage?: StoragePort;
  private epoch = 0;
  readonly pageRelease: string;
  constructor(pageRelease: string) {
    this.pageRelease = pageRelease;
    this.state = { version: 1, messages: [], result: null, requirements: unknownRequirements(), release_id: pageRelease, pending_turn: null, busy: false, checked: false, stale: false, storageLimited: false, notice: '' };
  }
  subscribe = (listener: () => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  snapshot = () => this.state;
  private update(patch: Partial<GuideState>, persist = true) {
    this.state = { ...this.state, ...patch };
    if (persist && this.storage) {
      const { version, messages, result, requirements, release_id, pending_turn } = this.state;
      try { this.storage.setItem(SESSION_KEY, JSON.stringify({ version, messages, result, requirements, release_id, pending_turn })); }
      catch { this.state = { ...this.state, storageLimited: true }; }
    }
    for (const listener of this.listeners) listener();
  }
  hydrate(storage?: StoragePort) {
    this.storage = storage;
    if (!storage) { this.update({ storageLimited: true }, false); return; }
    let raw;
    try { raw = storage.getItem(SESSION_KEY); } catch { this.update({ storageLimited: true }, false); return; }
    if (!raw) return;
    try {
      const saved = JSON.parse(raw);
      if (!saved || saved.version !== 1 || !keysOnly(saved, ['version', 'messages', 'result', 'requirements', 'release_id', 'pending_turn'])) throw Error('invalid session');
      const messages: GuideMessage[] = Array.isArray(saved.messages) ? saved.messages.filter((m: GuideMessage) => m && keysOnly(m, ['role', 'content']) && ['user', 'assistant'].includes(m.role) && typeof m.content === 'string' && [...m.content].length <= 16000) : [];
      let requirements = unknownRequirements();
      try { requirements = validateContract(requirementsSchema, structuredClone(saved.requirements)); } catch { /* Invalid local conditions cannot become selection input. */ }
      let result = null;
      let notice = '';
      try {
        if (saved.result !== null) {
          result = checkedResult(saved.result);
          if (JSON.stringify(result.confirmed_requirements) !== JSON.stringify(requirements)) throw Error('conditions differ');
        }
      } catch { result = null; notice = '已保存的结果不完整，请重新咨询或选择。'; }
      let pending: PendingTurn | null = null;
      if (saved.pending_turn !== null) {
        const p = saved.pending_turn;
        if (p && keysOnly(p, ['request_id', 'kind', 'text', 'selected_product_id', 'correcting']) && typeof p.request_id === 'string' && p.request_id && p.correcting === true && typeof p.text === 'string' && [...p.text].length <= 2000 && ((p.kind === 'consult' && p.text.trim() && p.selected_product_id === null) || (p.kind === 'select' && typeof p.selected_product_id === 'string' && p.selected_product_id))) pending = p;
        else { result = null; notice = '未完成记录无效，请重新咨询；旧结果已停用。'; }
      }
      const sameRelease = saved.release_id === this.pageRelease && (!result || result.release_id === this.pageRelease);
      this.update({ messages: plainHistory(messages), requirements, result: sameRelease ? result : null, release_id: this.pageRelease, pending_turn: pending, notice: sameRelease ? notice : '目录已更新，历史和条件已保留，请重新咨询或选择。' });
    } catch { this.update({ result: null, notice: '本标签页记录无法恢复，请重新咨询。' }); }
  }
  async recheck(currentRelease: () => Promise<string>) {
    const epoch = ++this.epoch;
    this.update({ checked: false });
    try {
      const current = await currentRelease();
      if (epoch !== this.epoch) return;
      if (current !== this.pageRelease) this.changed();
      else this.update({ checked: true });
    } catch { if (epoch === this.epoch) this.update({ notice: '暂时无法复核目录，操作已停用，请重试复核。' }); }
  }
  get operable() { return this.state.checked && !this.state.busy && !this.state.pending_turn && !this.state.stale; }
  begin(input: Omit<PendingTurn, 'request_id' | 'correcting'>, retry = false): Ticket {
    if (!this.state.checked || this.state.stale || this.state.busy || (this.state.pending_turn && !retry)) throw Error('请先处理未完成请求并复核目录。');
    const retryingSelection = retry && this.state.pending_turn?.kind === 'select' && this.state.pending_turn.selected_product_id === input.selected_product_id;
    if (input.kind === 'select' && !retryingSelection && (!this.state.result || !['recommendations', 'clarifying', 'selected'].includes(this.state.result.state))) throw Error('请重新咨询后选择。');
    const id = crypto.randomUUID();
    const ticket = { id, epoch: ++this.epoch };
    this.update({ pending_turn: { ...input, request_id: id, correcting: true }, busy: true, notice: '', messages: input.kind === 'consult' && !retry ? plainHistory([...this.state.messages, { role: 'user', content: input.text }]) : this.state.messages });
    return ticket;
  }
  private owns(ticket: Ticket) { return ticket.epoch === this.epoch && this.state.pending_turn?.request_id === ticket.id && this.state.busy; }
  fail(ticket: Ticket, notice: string) {
    if (!this.owns(ticket)) return;
    ++this.epoch;
    this.update({ busy: false, notice });
  }
  changed() { ++this.epoch; this.update({ busy: false, checked: false, stale: true, result: null, notice: '目录已更新，请刷新资料后重新提交。' }); }
  async commit(ticket: Ticket, value: unknown, text: string, currentRelease: () => Promise<string>) {
    if (!this.owns(ticket)) return false;
    const result = checkedResult(value);
    if (result.release_id !== this.pageRelease || result.state === 'unavailable') throw Error('结果版本无效或导购暂不可用。');
    const current = await currentRelease();
    if (!this.owns(ticket)) return false;
    if (current !== this.pageRelease) { this.changed(); return false; }
    const messages = text ? plainHistory([...this.state.messages, { role: 'assistant', content: text }]) : this.state.messages;
    this.update({ result, requirements: result.confirmed_requirements, release_id: result.release_id, pending_turn: null, busy: false, checked: true, messages, notice: '' });
    return true;
  }
  async discard(currentRelease: () => Promise<string>) {
    if (this.state.busy) return;
    ++this.epoch;
    this.update({ pending_turn: null, checked: false, notice: '已撤销未完成请求，正在复核目录。' });
    await this.recheck(currentRelease);
    if (this.state.checked) this.update({ notice: '已明确撤销未完成请求，保留上次完整条件。' });
  }
  clear() {
    ++this.epoch;
    this.update({ messages: [], result: null, requirements: unknownRequirements(), pending_turn: null, busy: false, notice: '' }, false);
    try { this.storage?.removeItem(SESSION_KEY); } catch { this.update({ storageLimited: true }, false); }
  }
}
