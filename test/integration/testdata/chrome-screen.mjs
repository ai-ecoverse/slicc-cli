import { headlessByDefault, modeFlags, screenFor } from '@ai-ecoverse/slicc-shared-web/harness';

const headless = headlessByDefault();
const screen = await screenFor(!headless);
process.stdout.write(`${JSON.stringify({ flags: modeFlags(headless), display: screen.env.DISPLAY ?? '' })}\n`);
process.stdin.resume();
await new Promise((resolve) => process.stdin.once('end', resolve));
await screen.close();
