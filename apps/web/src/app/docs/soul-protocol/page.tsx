'use client';

import DocSidebar from '@/components/DocSidebar';
import Navigation from '@/components/Navigation';
import { FiCopy } from 'react-icons/fi';
import { toast } from 'sonner';

export default function SoulProtocol() {
  const copyCode = (code: string) => {
    navigator.clipboard.writeText(code);
    toast.success('Code copied to clipboard');
  };

  return (
    <>
      <Navigation />
      <div className='min-h-screen bg-black'>
        <div className='pt-16'>
          <div className='flex flex-col lg:flex-row'>
            <DocSidebar />

            {/* Main Content */}
            <main className='min-w-0 flex-1 p-4 sm:p-6 lg:ml-64 lg:p-8'>
              <div className='max-w-4xl mx-auto'>
                <article className='text-gray-100'>
                  {/* Hero Section */}
                  <div className='bg-gradient-to-r from-blue-500/10 via-purple-500/10 to-blue-500/10 rounded-xl p-8 mb-12 border border-blue-500/20'>
                    <h1 className='text-4xl font-bold text-white mb-4'>Soul Protocol</h1>
                    <p className='text-xl text-gray-100'>
                      Discover the Soul Protocol, an advanced AI communication framework that enables intelligent agents
                      to collaborate and evolve within the Matrix OS ecosystem.
                    </p>
                  </div>

                  {/* Protocol Overview */}
                  <h2 className='text-3xl font-bold text-white mt-8 mb-6'>Protocol Overview</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800 mb-8'>
                    <p className='text-gray-100 leading-relaxed mb-4'>
                      The Soul Protocol is built on top of the Matrix Protocol, adding advanced AI capabilities:
                    </p>
                    <ul className='text-gray-100 space-y-3 list-disc pl-6'>
                      <li>AI Model Integration Framework</li>
                      <li>Federated Learning Capabilities</li>
                      <li>Knowledge Sharing Network</li>
                      <li>Autonomous Decision Making</li>
                      <li>Multi-Agent Collaboration</li>
                    </ul>
                  </div>

                  {/* Core Components */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Core Components</h2>

                  <div className='space-y-8'>
                    {/* AI Integration */}
                    <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-3'>AI Integration</h3>
                      <p className='text-gray-100 leading-relaxed mb-4'>Integrate AI models with the Soul Protocol:</p>
                      <div className='bg-black rounded-lg p-4'>
                        <div className='flex justify-between items-center mb-2'>
                          <span className='text-sm text-gray-100'>AI Integration Example</span>
                          <button
                            onClick={() =>
                              copyCode(`import { SoulProtocol, AIModel } from '@matrix-os/soul';

@AIModel({
  type: 'transformer',
  capabilities: ['nlp', 'reasoning']
})
class LanguageModel {
  async process(input: string): Promise<string> {
    // Model processing logic
    return this.transformer.generate(input);
  }
}

class IntelligentAgent extends SoulProtocol {
  private model: LanguageModel;

  async initialize() {
    this.model = await AIModel.load('language-model');
  }

  async handleQuery(query: string) {
    const response = await this.model.process(query);
    return this.formatResponse(response);
  }
}`)
                            }
                            className='p-2 hover:bg-gray-800 rounded transition-colors'
                          >
                            <FiCopy className='w-4 h-4' />
                          </button>
                        </div>
                        <pre className='text-sm text-gray-100 overflow-x-auto whitespace-pre-wrap break-words'>
                          <code>{`import { SoulProtocol, AIModel } from '@matrix-os/soul';

@AIModel({
  type: 'transformer',
  capabilities: ['nlp', 'reasoning']
})
class LanguageModel {
  async process(input: string): Promise<string> {
    // Model processing logic
    return this.transformer.generate(input);
  }
}

class IntelligentAgent extends SoulProtocol {
  private model: LanguageModel;

  async initialize() {
    this.model = await AIModel.load('language-model');
  }

  async handleQuery(query: string) {
    const response = await this.model.process(query);
    return this.formatResponse(response);
  }
}`}</code>
                        </pre>
                      </div>
                    </div>

                    {/* Knowledge Sharing */}
                    <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-3'>Knowledge Sharing</h3>
                      <p className='text-gray-100 leading-relaxed mb-4'>Enable knowledge sharing between agents:</p>
                      <div className='bg-black rounded-lg p-4'>
                        <div className='flex justify-between items-center mb-2'>
                          <span className='text-sm text-gray-100'>Knowledge Sharing Example</span>
                          <button
                            onClick={() =>
                              copyCode(`import { KnowledgeNetwork } from '@matrix-os/soul';

class SharedKnowledge extends KnowledgeNetwork {
  async shareInsight(insight: any) {
    // Validate and prepare insight
    const validatedInsight = await this.validate(insight);
    
    // Share with the network
    await this.broadcast({
      type: 'new_insight',
      data: validatedInsight,
      metadata: {
        source: this.agentId,
        confidence: this.calculateConfidence(insight),
        timestamp: Date.now()
      }
    });
  }

  async onInsightReceived(insight: any) {
    // Verify and integrate new knowledge
    if (await this.verifyInsight(insight)) {
      await this.integrateKnowledge(insight);
    }
  }
}`)
                            }
                            className='p-2 hover:bg-gray-800 rounded transition-colors'
                          >
                            <FiCopy className='w-4 h-4' />
                          </button>
                        </div>
                        <pre className='text-sm text-gray-100 overflow-x-auto whitespace-pre-wrap break-words'>
                          <code>{`import { KnowledgeNetwork } from '@matrix-os/soul';

class SharedKnowledge extends KnowledgeNetwork {
  async shareInsight(insight: any) {
    // Validate and prepare insight
    const validatedInsight = await this.validate(insight);
    
    // Share with the network
    await this.broadcast({
      type: 'new_insight',
      data: validatedInsight,
      metadata: {
        source: this.agentId,
        confidence: this.calculateConfidence(insight),
        timestamp: Date.now()
      }
    });
  }

  async onInsightReceived(insight: any) {
    // Verify and integrate new knowledge
    if (await this.verifyInsight(insight)) {
      await this.integrateKnowledge(insight);
    }
  }
}`}</code>
                        </pre>
                      </div>
                    </div>

                    {/* Autonomous Decision Making */}
                    <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                      <h3 className='text-xl font-bold text-white mb-3'>Decision Making</h3>
                      <p className='text-gray-100 leading-relaxed mb-4'>
                        Implement autonomous decision-making capabilities:
                      </p>
                      <ul className='text-gray-100 space-y-3 list-disc pl-6'>
                        <li>Multi-factor analysis</li>
                        <li>Risk assessment</li>
                        <li>Goal-oriented planning</li>
                        <li>Adaptive learning</li>
                      </ul>
                    </div>
                  </div>

                  {/* Protocol Features */}
                  <h2 className='text-3xl font-bold text-white mt-12 mb-6'>Protocol Features</h2>
                  <div className='bg-gray-900/50 rounded-xl p-6 border border-gray-800'>
                    <h3 className='text-xl font-bold text-white mb-3'>Key Features</h3>
                    <div className='grid grid-cols-1 md:grid-cols-2 gap-6'>
                      <div>
                        <h4 className='text-lg font-semibold text-white mb-2'>Learning Capabilities</h4>
                        <ul className='text-gray-100 space-y-2 list-disc pl-6'>
                          <li>Federated learning support</li>
                          <li>Transfer learning</li>
                          <li>Continuous adaptation</li>
                          <li>Experience sharing</li>
                        </ul>
                      </div>
                      <div>
                        <h4 className='text-lg font-semibold text-white mb-2'>Collaboration Features</h4>
                        <ul className='text-gray-100 space-y-2 list-disc pl-6'>
                          <li>Multi-agent coordination</li>
                          <li>Task delegation</li>
                          <li>Resource sharing</li>
                          <li>Collective intelligence</li>
                        </ul>
                      </div>
                    </div>
                  </div>

                  {/* Next Steps */}
                  <div className='bg-blue-500/10 rounded-xl p-6 mt-8 border border-blue-500/20'>
                    <h2 className='text-2xl font-bold text-white mb-4'>Next Steps</h2>
                    <p className='text-gray-100 leading-relaxed mb-4'>To start working with the Soul Protocol:</p>
                    <ul className='text-gray-100 space-y-3 list-disc pl-6 mb-0'>
                      <li>
                        <a href='/docs/matrix-protocol' className='text-blue-400 hover:text-blue-300 underline'>
                          Review the Matrix Protocol basics
                        </a>
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
