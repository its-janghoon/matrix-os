import { describe, expect, it } from 'vitest';
import type { ThreadMessage } from '@assistant-ui/react';

import { transcriptOf } from './runtime';

// What this function returns is what gets SIGNED - the run authorization covers
// a digest of it, and so does the seller's receipt. So a part that leaks in or
// falls out here is not a display bug: it is a signature over something the
// buyer did not send, and a receipt that then cannot be checked.
function message(role: ThreadMessage['role'], content: ThreadMessage['content']): ThreadMessage {
  return { id: role + Math.random(), role, content, createdAt: new Date(), metadata: {} } as ThreadMessage;
}

describe('the transcript that gets signed', () => {
  it('carries text, in order, for both sides', () => {
    expect(
      transcriptOf([
        message('user', [{ type: 'text', text: 'hello' }]),
        message('assistant', [{ type: 'text', text: 'hi' }]),
        message('user', [{ type: 'text', text: 'and again' }]),
      ] as ThreadMessage[]),
    ).toEqual([
      { role: 'user', content: 'hello' },
      { role: 'assistant', content: 'hi' },
      { role: 'user', content: 'and again' },
    ]);
  });

  // A reasoning model's working is the model's, not the buyer's. Replaying it
  // as conversation would sign a prompt the buyer never wrote, and the digest
  // the seller receipts would no longer be the one this page can recompute.
  it('leaves the model working out of what is sent back', () => {
    expect(
      transcriptOf([
        message('assistant', [
          { type: 'reasoning', text: 'let me think about this at length' },
          { type: 'text', text: 'the answer' },
        ]),
      ] as ThreadMessage[]),
    ).toEqual([{ role: 'assistant', content: 'the answer' }]);
  });

  // A message that is still running has no text yet. Sending an empty turn
  // would spend a signature on nothing.
  it('drops a message with no text at all', () => {
    expect(transcriptOf([message('assistant', []), message('user', [{ type: 'text', text: 'x' }])] as ThreadMessage[]))
      .toEqual([{ role: 'user', content: 'x' }]);
  });

  it('joins a message split across several text parts', () => {
    expect(
      transcriptOf([
        message('user', [
          { type: 'text', text: 'one ' },
          { type: 'text', text: 'two' },
        ]),
      ] as ThreadMessage[]),
    ).toEqual([{ role: 'user', content: 'one two' }]);
  });
});
