/**
 * The column every app page's content sits in.
 *
 * It exists because each page had picked its own width - the marketplace 6xl,
 * the bridge 4xl, chat 3xl - so moving between them shifted the content's edges
 * under a navigation bar that never moved. A reader reads that as three
 * different sites rather than three parts of one, and nothing in the code said
 * which width was the intended one, so every new page picked again.
 *
 * Marketing and docs pages are deliberately NOT on this. A docs page is a column
 * of prose inside its own sidebar frame, and prose set to 1152px is harder to
 * read, not more consistent - a long line makes the eye lose its place on the
 * return sweep. This is the width for the pages that show tables, panels and
 * forms, where the content wants the room.
 *
 * Prose inside these pages keeps its own narrower measure for the same reason.
 */
export const PAGE_COLUMN = 'mx-auto max-w-6xl';
