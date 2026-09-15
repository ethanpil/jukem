import { h, clear } from '../dom.js';

// playlistsView arrives in a later build step.
export async function playlistsView(main) {
  clear(main);
  main.append(h('p.text-body-secondary', 'The playlists screen arrives in a later build step.'));
  return {};
}
