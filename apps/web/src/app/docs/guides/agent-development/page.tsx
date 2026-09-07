import { CodeSample } from '@/components/CodeSample';
import DocSidebar from '@/components/DocSidebar';
import Navigation from '@/components/Navigation';
import { GITHUB_URL } from '@/lib/releases';

export const metadata = {
  title: 'Agent development',
  description:
    'A node embeds a WebAssembly runtime with four host functions. This page documents that ABI, and the deployment path that does not exist yet.',
};

/**
 * This page used to show an `Agent` class and an `AgentContext` imported from
 * `@matrix-os/core`, with lifecycle hooks that appear nowhere in this
 * repository. What a node actually has is a wazero WebAssembly runtime with
 * four host functions and a fuel and memory budget - and no way to load a
 * module into it from outside the process. Both halves of that are documented
 * here.
 */
const HOST_ABI = `// Imported by the guest module from the host module "env":
log(offset: u32, length: u32)
send(target_offset: u32, target_length: u32, msg_offset: u32, msg_length: u32)
get_memory(offset: u32, length: u32)
set_memory(offset: u32, length: u32)`;

const LIMITS = `// services/core/internal/agent/agent.go
var DefaultMemoryLimits = ResourceLimits{
    MaxMemoryPages: 256,      // 256 * 64KB = 16MB
    MaxFuel:        1000000,  // execution budget
}`;

const LOAD = `// From Go, inside the process:
a, err := agent.New(ctx, agent.Config{
    ID:     "my-agent",
    Code:   wasmBytes,   // a compiled module
    Stdout: os.Stdout,
    Stderr: os.Stderr,
}, agent.DefaultMemoryLimits)
if err != nil { return err }

if err := a.Start(ctx); err != nil { return err }
defer a.Stop(ctx)`;

const RUST = `# A guest module can be written in any language that targets wasm.
# Rust, for example:
cargo new --lib my-agent
# Cargo.toml: crate-type = ["cdylib"]
cargo build --target wasm32-unknown-unknown --release
# -> target/wasm32-unknown-unknown/release/my_agent.wasm`;

export default function AgentDevelopmentPage() {
  return (
    <>
      <Navigation />
      <div className='min-h-screen bg-black'>
        <div className='pt-16'>
          <div className='flex flex-col lg:flex-row'>
            <DocSidebar />

            <main className='min-w-0 flex-1 p-4 sm:p-6 lg:ml-64 lg:p-8'>
              <div className='mx-auto max-w-4xl'>
                <article className='text-gray-100'>
                  <div className='mb-10 rounded-xl border border-primary-400/20 bg-gradient-to-r from-primary-400/10 via-accent-300/10 to-primary-400/10 p-8'>
                    <h1 className='mb-4 text-4xl font-bold text-white'>Agent development</h1>
                    <p className='text-xl text-gray-100'>
                      A node embeds a WebAssembly runtime. An agent is a wasm module with a fuel budget, a memory
                      cap, and four host functions.
                    </p>
                  </div>

                  <div className='mb-10 rounded-xl border border-semantic-processing/40 bg-semantic-processing/10 p-6'>
                    <h2 className='mb-2 text-xl font-bold text-white'>Status: the runtime exists, the deployment path does not</h2>
                    <p className='mb-0 text-gray-100'>
                      The runtime below is real and runs modules today, from Go, in-process. What is missing is a way
                      to get a module into it from outside: the admin service&apos;s{' '}
                      <code className='text-white'>DeployAgent</code> records a deployment in a map and reports it as
                      running, but it never loads or executes any wasm. Until that is wired up, agents are a library
                      feature rather than a product one, and this page will not pretend otherwise.
                    </p>
                  </div>

                  <h2 className='mb-4 mt-8 text-3xl font-bold text-white'>The runtime</h2>
                  <p className='mb-4 text-gray-300'>
                    <a
                      href='https://wazero.io'
                      className='text-accent-200 underline hover:text-accent-100'
                      target='_blank'
                      rel='noopener noreferrer'
                    >
                      wazero
                    </a>
                    , which is a pure-Go wasm runtime: no CGO, no external toolchain in the node. Every module gets a
                    memory ceiling and a fuel budget, so a runaway agent costs its own budget rather than the
                    node&apos;s.
                  </p>
                  <CodeSample label='Go' code={LIMITS} />

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>The host ABI</h2>
                  <p className='mb-4 text-gray-300'>
                    Four functions, and that is the whole surface a guest sees. An agent cannot open a socket, read a
                    file or spawn anything - not by policy but by construction, because nothing else is imported into
                    the module.
                  </p>
                  <CodeSample label='host functions' code={HOST_ABI} />
                  <p className='mt-4 text-gray-300'>
                    Each takes a pointer and a length into the guest&apos;s own linear memory, which is the usual wasm
                    convention for passing bytes across the boundary.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>Building a module</h2>
                  <CodeSample label='shell' code={RUST} />
                  <p className='mt-4 text-gray-300'>
                    Any language that compiles to <code className='text-white'>wasm32</code> works - Rust, Go via
                    TinyGo, C, Zig. The module only has to import the four functions above and export what the host
                    starts.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>Loading one</h2>
                  <CodeSample label='Go' code={LOAD} />
                  <p className='mt-4 text-gray-300'>
                    See{' '}
                    <a
                      href={`${GITHUB_URL}/tree/main/services/core/internal/agent`}
                      className='text-accent-200 underline hover:text-accent-100'
                      target='_blank'
                      rel='noopener noreferrer'
                    >
                      <code className='text-white'>internal/agent</code>
                    </a>{' '}
                    for the runtime itself.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>What has to be built next</h2>
                  <ul className='list-disc space-y-3 pl-6 text-gray-300'>
                    <li>
                      Wire <code className='text-white'>DeployAgent</code> to the runtime, so an operator can upload a
                      module rather than recompile the node.
                    </li>
                    <li>Persist deployments, so an agent survives a restart.</li>
                    <li>
                      Meter fuel against the marketplace, so running an agent costs MATRIX the way a compute job
                      does. Today the fuel budget protects the node but bills nobody.
                    </li>
                    <li>
                      Decide what <code className='text-white'>send</code> may address. A host function that can
                      message other agents needs a policy before it can be exposed to untrusted modules.
                    </li>
                  </ul>

                  <div className='mt-10 rounded-xl border border-primary-400/20 bg-primary-400/10 p-6'>
                    <h2 className='mb-4 text-2xl font-bold text-white'>Next</h2>
                    <ul className='mb-0 list-disc space-y-3 pl-6 text-gray-100'>
                      <li>
                        <a href='/docs/quickstart' className='text-accent-200 underline hover:text-accent-100'>
                          Quickstart
                        </a>{' '}
                        - the parts of the stack that are finished
                      </li>
                      <li>
                        <a href='/docs/soul-protocol' className='text-accent-200 underline hover:text-accent-100'>
                          Soul Protocol
                        </a>{' '}
                        - the identity layer agents are meant to use, also unserved
                      </li>
                    </ul>
                  </div>
                </article>
              </div>
            </main>
          </div>
        </div>
      </div>
    </>
  );
}
