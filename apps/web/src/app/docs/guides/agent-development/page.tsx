import { CodeSample } from '@/components/CodeSample';
import DocSidebar from '@/components/DocSidebar';
import Navigation from '@/components/Navigation';
import { GITHUB_URL } from '@/lib/releases';

export const metadata = {
  title: 'Agent development',
  description:
    'A node embeds a WebAssembly runtime with four host functions, a memory ceiling and a run-time deadline. This page documents that ABI and how to load a module into it.',
};

/**
 * This page used to show an `Agent` class and an `AgentContext` imported from
 * `@matrix-os/core`, with lifecycle hooks that appear nowhere in this
 * repository.
 *
 * It then documented the real runtime, but described an ABI that did not work:
 * all four host functions were empty stubs, so a guest could call log() and get
 * silence. The "fuel budget" was a struct field nothing read. Both are fixed in
 * the code now, and this page describes what the code does.
 */
const HOST_ABI = `// Imported by the guest module from the host module "env":
log(offset: u32, length: u32)
send(target_offset: u32, target_length: u32, msg_offset: u32, msg_length: u32)
get_memory(offset: u32, length: u32)
set_memory(offset: u32, length: u32)`;

const LIMITS = `// services/core/internal/agent/agent.go
var DefaultMemoryLimits = ResourceLimits{
    MaxMemoryPages: 256,             // 256 * 64KB = 16MB
    MaxRunTime:     5 * time.Second, // per call into the guest
}`;

const LOAD = `// From Go, inside the process:
a, err := agent.New(ctx, agent.Config{
    ID:     "my-agent",
    Code:   wasmBytes,   // a compiled module
    Stdout: os.Stdout,
    Stderr: os.Stderr,
    // Nil refuses every send() the guest makes, and says so on stderr.
    Send:   func(target string, payload []byte) error { return nil },
}, agent.DefaultMemoryLimits)
if err != nil { return err }

if err := a.Start(ctx); err != nil { return err }   // runs _start under MaxRunTime
defer a.Stop(ctx)

out, err := a.Call(ctx, "some_export")   // any export, same deadline
a.Memory()                               // the host-side buffer set_memory writes
a.Sent()                                 // every send() the guest attempted`;

const DEPLOY = `// Or through the admin service, from a module on the node or inline:
deploySvc.DeployAgent(ctx, "my-agent", map[string]interface{}{
    "wasm_path": "/srv/agents/my-agent.wasm",
    // or: "wasm_base64": "<the module, base64>"
})

deploySvc.Agent("my-agent")              // the loaded runtime, to call exports
deploySvc.StopDeployment(ctx, "my-agent")  // closes the module and its runtime`;

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
                      A node embeds a WebAssembly runtime. An agent is a wasm module with a memory cap, a run-time
                      deadline, and four host functions - and nothing else.
                    </p>
                  </div>

                  <div className='mb-10 rounded-xl border border-semantic-processing/40 bg-semantic-processing/10 p-6'>
                    <h2 className='mb-2 text-xl font-bold text-white'>Status: it runs, and it is not yet a product</h2>
                    <p className='mb-0 text-gray-100'>
                      The runtime runs modules, the four host functions below do what they say, and{' '}
                      <code className='text-white'>DeployAgent</code> loads and runs a module you give it - it used
                      to record a deployment in a map, report it running, and never execute a byte of wasm. What is
                      still missing is the product around it: no gRPC surface exposes{' '}
                      <code className='text-white'>DeployAgent</code>, so a module has to come from inside the
                      process; deployments do not survive a restart; and running an agent costs nobody anything,
                      because the runtime is not metered against the marketplace. Agents are a library feature, not
                      a product one, and this page will not pretend otherwise.
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
                    memory ceiling and a per-call deadline, so a guest that never returns is torn down instead of
                    holding the calling goroutine forever.
                  </p>
                  <p className='mb-4 text-gray-300'>
                    The deadline is what a <code className='text-white'>MaxFuel</code> field used to promise here.
                    wazero has no instruction meter to spend a fuel budget against, so that number was decoration
                    and <code className='text-white'>{'for {}'}</code> in a guest ran until the process died. A
                    deadline is a bound the runtime can enforce, and it enforces the property that matters. Exceeding
                    it returns an error wrapping <code className='text-white'>context.DeadlineExceeded</code>, and
                    the module is not usable afterwards.
                  </p>
                  <CodeSample label='Go' code={LIMITS} />

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>What a guest cannot exhaust</h2>
                  <p className='mb-4 text-gray-300'>
                    A deadline bounds one call. It does not bound what a guest ACCUMULATES inside the deadline, and
                    that turned out to be the larger hole. A guest logging a 60 KiB line in a loop wrote{' '}
                    <strong>586 MiB</strong> from one run; a guest calling <code className='text-white'>send</code>{' '}
                    in a loop with no send policy - the secure default, where every send is refused - held{' '}
                    <strong>1172 MiB</strong>, because the refusals were recorded in full. Both numbers are measured,
                    from purpose-built guests that are now fixtures in the test suite.
                  </p>
                  <p className='mb-4 text-gray-300'>
                    So the log and the send record are byte-budgeted as well as line-capped, and a guest past its
                    budget has output dropped rather than the node growing. Ask{' '}
                    <code className='text-white'>SendsDropped()</code> whether that happened: silence and success
                    look identical from inside the guest, which is deliberate - a guest must not be able to tell how
                    close it is to a limit and pace itself against it.
                  </p>
                  <p className='mb-4 text-gray-300'>
                    The limits you pass are also <strong>clamped, not trusted</strong>. A deployment asking for the
                    wasm maximum of 65536 pages (4 GiB) gets 1024, and a run-time of an hour gets 30 seconds. The
                    inbox is bounded in both messages and bytes. A limit an operator can raise without bound is not
                    a limit, and a deployment request is not an operator decision.
                  </p>
                  <p className='mb-4 text-gray-300'>
                    A guest also <strong>cannot read a clock</strong>. No WASI module is instantiated and none of the
                    four host functions returns a value, so there is no timer and no reply channel to build one from.
                    That is a deliberate boundary rather than an omission: a guest runs in the node&apos;s own process
                    beside the validator&apos;s signing key, and a nanosecond timer would let it time its own host
                    calls and turn any data-dependent branch in the host into a side channel without breaking a
                    single other limit. A module that imports{' '}
                    <code className='text-white'>wasi_snapshot_preview1</code> fails to load.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>The host ABI</h2>
                  <p className='mb-4 text-gray-300'>
                    Four functions, and that is the whole surface a guest sees. An agent cannot open a socket, read a
                    file, read a clock or spawn anything - not by policy but by construction, because nothing else is
                    imported into the module. A module that asks for a fifth import does not load.
                  </p>
                  <CodeSample label='host functions' code={HOST_ABI} />
                  <p className='mt-4 text-gray-300'>
                    Each takes a pointer and a length into the guest&apos;s own linear memory, which is the usual wasm
                    convention for passing bytes across the boundary. None of them trusts those numbers: a range that
                    is not wholly inside the guest&apos;s memory is a refused call, reported on the agent&apos;s
                    stderr, rather than a read of whatever sits next to it. The ABI has no return values, so a guest
                    cannot be handed an error - a refusal is visible to the operator, not to the module.
                  </p>
                  <ul className='mt-4 list-disc space-y-3 pl-6 text-gray-300'>
                    <li>
                      <code className='text-white'>log</code> writes the guest&apos;s bytes to the agent&apos;s
                      stdout, tagged with its id, capped at 10,000 lines so a guest in a loop cannot fill a disk
                      through the host&apos;s logger.
                    </li>
                    <li>
                      <code className='text-white'>set_memory</code> and{' '}
                      <code className='text-white'>get_memory</code> move bytes between the guest and a host-side
                      buffer that outlives a call. The buffer is deliberately not addressable by the guest: those two
                      functions are the only way in and out of it.
                    </li>
                    <li>
                      <code className='text-white'>send</code> hands a target and a payload to the{' '}
                      <code className='text-white'>SendFunc</code> the host configured. Who a module may address is a
                      policy question, so it is a deliberate, configurable send policy with a secure default rather
                      than an invented one. By default inter-agent send is <strong>off</strong>: every send is refused
                      and reported on the guest&apos;s stderr, and the attempt is recorded either way so you can see
                      what a module tried to do. An operator opts in by setting{' '}
                      <code className='text-white'>agent.allow_send</code> and naming an allowlist in{' '}
                      <code className='text-white'>agent.send_allowlist</code>; a <em>name</em> is a deployment id on
                      the same node, and a permitted send is delivered into that agent&apos;s inbox. There is no
                      wildcard and nothing off-node is addressable.
                    </li>
                  </ul>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>Building a module</h2>
                  <CodeSample label='shell' code={RUST} />
                  <p className='mt-4 text-gray-300'>
                    Any language that compiles to <code className='text-white'>wasm32</code> works - Rust, Go via
                    TinyGo, C, Zig. The module only has to import the four functions above and export what the host
                    starts.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>Loading one</h2>
                  <CodeSample label='Go' code={LOAD} />
                  <p className='mb-4 mt-4 text-gray-300'>
                    Or through the admin deploy service, which takes the module as a path on the node or inline as
                    base64:
                  </p>
                  <CodeSample label='Go' code={DEPLOY} />
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
                      Expose <code className='text-white'>DeployAgent</code> over gRPC, so an operator can upload a
                      module instead of driving the service from inside the process. It loads and runs one today;
                      nothing outside the node can ask it to.
                    </li>
                    <li>Persist deployments, so an agent survives a restart.</li>
                    <li>
                      Meter execution against the marketplace, so running an agent costs MATRIX the way a compute job
                      does. Today the deadline protects the node but bills nobody.
                    </li>
                    <li>
                      Route <code className='text-white'>send</code> off-node. Inter-agent send now has a real,
                      configurable policy (a name resolves to another agent&apos;s inbox on the same node, refused by
                      default, opt-in via <code className='text-white'>agent.allow_send</code> and{' '}
                      <code className='text-white'>agent.send_allowlist</code>). What is still unbuilt is addressing an
                      agent on a <em>different</em> node: the primitive is deliberately local for now.
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
