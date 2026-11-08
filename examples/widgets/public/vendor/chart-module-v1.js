// Application adapter: native transitive import, default/named exports and a
// live binding shared by eager and lazy Go components in one document.
export {default} from './uPlot-1.6.32.esm.js';
export const libraryVersion = '1.6.32';
export let mountCount = 0;
export function recordMount() { return ++mountCount; }
