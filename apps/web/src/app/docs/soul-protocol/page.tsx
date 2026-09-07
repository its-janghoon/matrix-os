import { CodeSample } from '@/components/CodeSample';
import DocSidebar from '@/components/DocSidebar';
import Navigation from '@/components/Navigation';
import { GITHUB_URL } from '@/lib/releases';

export const metadata = {
  title: 'Soul Protocol',
  description:
    'The Soul Protocol as it stands: Protocol Buffers definitions and in-process Go types. No service serves it yet, and this page says so.',
};

/**
 * This page used to show `new SoulProtocol({...})` and a `KnowledgeNetwork`
 * imported from an `@matrix-os/soul` npm package. There is no such package, and
 * no node serves these services. What exists is the proto definitions and a
 * small in-process Go type. Documenting that honestly is more useful than
 * example code for an API nobody can call.
 */
const SERVICES = `matrix.soul.v1.SoulLifecycleService   CreateSoul, GetSoul, UpdateSoul, DeleteSoul,
                                      StartTraining, StopTraining, GetTrainingStatus,
                                      AddTrainingExamples
matrix.soul.v1.MemoryService          memory read/write
matrix.soul.v1.ValueService           value weights
matrix.soul.v1.GoalService            goals
matrix.soul.v1.SoulChatService        conversation
matrix.soul.v1.InferenceService       soul-scoped inference`;

const GO_TYPE = `// services/core/internal/soul/soul.go
type Soul struct {
    ID      string
    memory  []MemoryEntry   // timestamp, content, type, tags
    values  map[string]float64
    persona Persona         // traits, goals
}`;

export default function SoulProtocolPage() {
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
                  <div className='mb-10 rounded-xl border border-accent-300/20 bg-gradient-to-r from-accent-300/10 via-primary-400/10 to-accent-300/10 p-8'>
                    <h1 className='mb-4 text-4xl font-bold text-white'>Soul Protocol</h1>
                    <p className='text-xl text-gray-100'>
                      Persistent identity, memory and values for an agent, defined as Protocol Buffers services.
                    </p>
                  </div>

                  <div className='mb-10 rounded-xl border border-semantic-processing/40 bg-semantic-processing/10 p-6'>
                    <h2 className='mb-2 text-xl font-bold text-white'>Status: defined, not served</h2>
                    <p className='mb-0 text-gray-100'>
                      The services below exist as <code className='text-white'>.proto</code> definitions, and a small
                      in-process Go type holds a soul&apos;s memory, values and persona. <strong>No node serves any
                      of these RPCs today</strong>, and there is no client library for them. If you are looking for
                      something to build against right now, that is the{' '}
                      <a href='/docs/compute-marketplace' className='text-accent-200 underline hover:text-accent-100'>
                        compute marketplace
                      </a>{' '}
                      and{' '}
                      <a href='/products/inference' className='text-accent-200 underline hover:text-accent-100'>
                        inference
                      </a>{' '}
                      APIs, which a node does serve.
                    </p>
                  </div>

                  <h2 className='mb-4 mt-8 text-3xl font-bold text-white'>What the protocol defines</h2>
                  <CodeSample label='services' code={SERVICES} />
                  <p className='mt-4 text-gray-300'>
                    The definitions live in{' '}
                    <a
                      href={`${GITHUB_URL}/tree/main/proto/matrix/soul/v1`}
                      className='text-accent-200 underline hover:text-accent-100'
                      target='_blank'
                      rel='noopener noreferrer'
                    >
                      <code className='text-white'>proto/matrix/soul/v1</code>
                    </a>
                    . They are buf-managed and generate Go stubs, so implementing them is a matter of writing the
                    service, not of designing the wire format.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>What exists in the node</h2>
                  <CodeSample label='Go' code={GO_TYPE} />
                  <p className='mt-4 text-gray-300'>
                    A node holds these in a map, keyed by id. They are reachable from Go code inside the process and
                    from nowhere else: no gRPC service is registered for them, so they are not on the network and not
                    persisted to the store.
                  </p>

                  <h2 className='mb-4 mt-12 text-3xl font-bold text-white'>What is missing</h2>
                  <ul className='list-disc space-y-3 pl-6 text-gray-300'>
                    <li>A service implementation for any of the six definitions.</li>
                    <li>Persistence: a soul currently lives in memory and dies with the process.</li>
                    <li>
                      A settlement story. Training and inference against a soul cost compute, and nothing connects
                      them to the marketplace that charges for it.
                    </li>
                    <li>Authorisation: who may read another account&apos;s soul memory, and on what terms.</li>
                  </ul>

                  <div className='mt-10 rounded-xl border border-primary-400/20 bg-primary-400/10 p-6'>
                    <h2 className='mb-4 text-2xl font-bold text-white'>Next</h2>
                    <ul className='mb-0 list-disc space-y-3 pl-6 text-gray-100'>
                      <li>
                        <a href='/docs/matrix-protocol' className='text-accent-200 underline hover:text-accent-100'>
                          Matrix Protocol
                        </a>{' '}
                        - the protocol that is implemented and running
                      </li>
                      <li>
                        <a
                          href='/docs/guides/agent-development'
                          className='text-accent-200 underline hover:text-accent-100'
                        >
                          Agent development
                        </a>{' '}
                        - the WebAssembly runtime a node actually has
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
