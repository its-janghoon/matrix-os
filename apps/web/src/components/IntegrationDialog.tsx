import Image from 'next/image';
import Button from './Button';
import Dialog from './Dialog';

interface Integration {
  id: string;
  name: string;
  description: string;
  status: 'connected' | 'disconnected' | 'pending';
  icon: string;
}

interface IntegrationDialogProps {
  isOpen: boolean;
  onClose: () => void;
}

const integrations: Integration[] = [
  {
    id: 'gmail',
    name: 'Gmail',
    description: 'Your AI twin can read and respond to emails on your behalf, learning your communication style',
    status: 'connected',
    icon: '/icons/gmail.svg',
  },
  {
    id: 'calendar',
    name: 'Google Calendar',
    description: 'Your digital self manages your schedule, booking meetings and setting reminders',
    status: 'connected',
    icon: '/icons/calendar.png',
  },
  {
    id: 'slack',
    name: 'Slack',
    description: 'Your AI assistant participates in team conversations, maintaining your presence',
    status: 'pending',
    icon: '/icons/slack.svg',
  },
  {
    id: 'notion',
    name: 'Notion',
    description: 'Your digital twin organizes and updates your knowledge base, keeping it current',
    status: 'connected',
    icon: '/icons/notion.png',
  },
  {
    id: 'spotify',
    name: 'Spotify',
    description: 'Your AI self curates playlists based on your preferences and current activities',
    status: 'disconnected',
    icon: '/icons/spotify.svg',
  },
];

export default function IntegrationDialog({ isOpen, onClose }: IntegrationDialogProps) {
  return (
    <Dialog isOpen={isOpen} onClose={onClose} title='Integrations' maxWidth='xl'>
      <div className='space-y-4'>
        {integrations.map(integration => (
          <div
            key={integration.id}
            className='bg-black/30 p-4 rounded-lg hover:bg-black/40 transition-all cursor-pointer'
          >
            <div className='flex items-center justify-between'>
              <div className='flex items-center space-x-3'>
                <div className='relative w-8 h-8'>
                  <Image
                    src={integration.icon}
                    alt={integration.name}
                    fill
                    className='object-contain'
                    onError={e => {
                      const target = e.target as HTMLImageElement;
                      target.src = '/icons/default.svg';
                    }}
                  />
                </div>
                <div className='flex-1'>
                  <h3 className='text-lg font-semibold'>{integration.name}</h3>
                  <p className='text-sm text-gray-300'>{integration.description}</p>
                </div>
              </div>
              <Button variant={integration.status === 'connected' ? 'danger' : 'primary'} size='sm'>
                {integration.status === 'connected' ? 'Disconnect' : 'Connect'}
              </Button>
            </div>
          </div>
        ))}
      </div>
    </Dialog>
  );
}
