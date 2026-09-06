import { X } from 'lucide-react';
import Button from './Button';

interface DialogProps {
  isOpen: boolean;
  onClose: () => void;
  title?: string;
  children: React.ReactNode;
  className?: string;
  maxWidth?: 'sm' | 'md' | 'lg' | 'xl' | 'full';
}

const maxWidthClasses = {
  sm: 'max-w-sm',
  md: 'max-w-md',
  lg: 'max-w-lg',
  xl: 'max-w-4xl',
  full: 'max-w-full',
};

export default function Dialog({ isOpen, onClose, title, children, className = '', maxWidth = 'md' }: DialogProps) {
  if (!isOpen) return null;

  return (
    <div className='fixed inset-0 bg-black/50 flex items-center justify-center z-50'>
      <div
        className={`bg-black/90 border border-gray-700 rounded-lg p-6 w-full ${maxWidthClasses[maxWidth]} relative ${className}`}
      >
        <div className='flex justify-between items-center mb-6'>
          {title && <h2 className='text-2xl font-bold'>{title}</h2>}
          <Button onClick={onClose} variant='ghost' size='sm' className='absolute top-1.5 right-1.5'>
            <X size={20} />
          </Button>
        </div>
        {children}
      </div>
    </div>
  );
}
