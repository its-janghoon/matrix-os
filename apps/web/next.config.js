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
