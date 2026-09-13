import { build } from 'esbuild';

await build({
  entryPoints: ['TUI/src/index.tsx'],
  bundle: true,
  platform: 'node',
  format: 'esm',
  target: 'node22',
  define: {
    'process.env.DEV': 'false',
  },
  alias: {
    'react-devtools-core': './TUI/src/react-devtools-core-shim.ts',
  },
  banner: {
    js: 'import { createRequire } from "node:module"; const require = createRequire(import.meta.url);',
  },
  outfile: 'TUI/dist/tui.mjs',
  logLevel: 'info',
});
