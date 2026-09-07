/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  env: {
    ENVIRONMENT: process.env.ENVIRONMENT,
    GA_TRACKING_ID: process.env.GA_TRACKING_ID,
  },
  // Still an experimental key in Next 16 (config-shared.d.ts lists
  // `serverActions` under `experimental`), so it stays where it was.
  experimental: {
    serverActions: {
      allowedOrigins: ['*'],
    },
  },
  // The canonical signing byte layouts live in one place, packages/protocol, and
  // are consumed here as a linked workspace package rather than copied. It ships
  // TypeScript source (no build step, no dist to go stale), so Next has to
  // transpile it. Without this the build fails on the untranspiled TS.
  //
  // A plain relative import across the app root does NOT work: tsc accepts it
  // and Turbopack refuses it ("Module not found"), so the package boundary is
  // the mechanism, not a convenience.
  transpilePackages: ['@matrix-os/protocol'],

  // Turbopack refuses to resolve a module that symlinks OUTSIDE its inferred
  // root, so the linked package above needs the root widened to the monorepo.
  // Found by probing: tsc accepted the import, and only the build failed.
  // __dirname rather than a require('path') call: this file is CommonJS and the
  // repo's eslint config forbids require-style imports.
  turbopack: {
    root: __dirname + '/../..',
  },

  // No `eslint` block: Next 16 removed the option along with `next lint`, and
  // `next build` no longer runs linting. Linting is `yarn lint` (the ESLint CLI)
  // and CI runs it as its own step.
  //
  // No `typescript.ignoreBuildErrors` either. It used to be set, which meant the
  // build never type-checked; the only errors it was hiding came from two
  // orphaned files under src/lib that imported packages this project does not
  // install. Those are gone, `tsc --noEmit` is clean, and the build now fails on
  // type errors like it should.
};

module.exports = nextConfig;
