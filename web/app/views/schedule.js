import { h, clear } from '../dom.js';

// scheduleView arrives in a later build step.
export async function scheduleView(main) {
  clear(main);
  main.append(h('p.text-body-secondary', 'The schedule screen arrives in a later build step.'));
  return {};
}
