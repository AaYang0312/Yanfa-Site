export function element<K extends keyof HTMLElementTagNameMap>(tag: K, text = '', className = '') {
  const el = document.createElement(tag); el.textContent = text; el.className = className; return el;
}
export function button(text: string, action: () => void | Promise<void>, className = '') {
  const el = element('button', text, className); el.type = 'button'; el.addEventListener('click', async () => {
    el.disabled = true;
    try { await action(); } finally { el.disabled = false; }
  }); return el;
}
export function input(label: string, value: string, change: (value: string) => void, options: { type?: string; max?: number; required?: boolean; multiline?: boolean; readOnly?: boolean } = {}) {
  const wrapper = element('label', label);
  const control = options.multiline ? element('textarea') : element('input');
  if (control instanceof HTMLInputElement) control.type = options.type ?? 'text';
  if (options.max) control.maxLength = options.max;
  control.value = value; control.required = options.required ?? false; control.readOnly = options.readOnly ?? false;
  control.addEventListener('input', () => change(control.value)); wrapper.append(control); return wrapper;
}
export function select(label: string, value: string, choices: Record<string, string>, change: (value: string) => void) {
  const wrapper = element('label', label); const control = element('select');
  for (const [key, text] of Object.entries(choices)) { const option = element('option', text); option.value = key; control.append(option); }
  control.setAttribute('aria-label', label);
  control.value = value; control.addEventListener('change', () => change(control.value)); wrapper.append(control); return wrapper;
}
export function checkbox(label: string, checked: boolean, change: (checked: boolean) => void) {
  const wrapper = element('label', '', 'check'); const control = element('input'); control.type = 'checkbox'; control.checked = checked;
  control.addEventListener('change', () => change(control.checked)); wrapper.append(control, document.createTextNode(label)); return wrapper;
}
export function reorder<T>(items: T[], index: number, render: () => void, mark: () => void) {
  const actions = element('div', '', 'actions');
  const up = button('上移', () => { [items[index - 1], items[index]] = [items[index], items[index - 1]]; mark(); render(); }); up.disabled = index === 0;
  const down = button('下移', () => { [items[index + 1], items[index]] = [items[index], items[index + 1]]; mark(); render(); }); down.disabled = index === items.length - 1;
  actions.append(up, down); return actions;
}
