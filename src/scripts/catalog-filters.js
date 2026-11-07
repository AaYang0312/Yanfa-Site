const filterNames = ['type', 'scene', 'nail'];
const normalize = (value) => value.toLowerCase().replace(/[\s_-]/g, '');

export function readCatalogState(params) {
  return {
    type: params.getAll('type'),
    scene: params.getAll('scene'),
    nail: params.getAll('nail'),
    q: params.get('q')?.trim() || '',
  };
}

export function writeCatalogState(state, current = '') {
  const params = new URLSearchParams(current);
  for (const name of [...filterNames, 'q']) params.delete(name);
  for (const name of filterNames) {
    for (const value of state[name]) params.append(name, value);
  }
  if (state.q.trim()) params.set('q', state.q.trim());
  return params;
}

export function matchesCatalogItem(item, state) {
  const groupsMatch = filterNames.every((name) => {
    const values = state[name];
    if (!values.length) return true;
    const own = Array.isArray(item[name]) ? item[name] : [item[name]];
    return values.some((value) => own.includes(value));
  });
  return groupsMatch && normalize(item.search || '').includes(normalize(state.q));
}
