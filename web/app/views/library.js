import { h, clear } from '../dom.js';

// libraryView arrives in a later build step.
export async function libraryView(main) {
  clear(main);
  main.append(h('p.text-body-secondary', 'The library screen arrives in a later build step.'));
  return {};
}
