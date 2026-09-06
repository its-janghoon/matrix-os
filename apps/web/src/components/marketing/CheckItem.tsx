import { FiCheck } from 'react-icons/fi';

const CheckItem = ({ children }: { children: React.ReactNode }) => (
  <li className='flex gap-3'>
    <span className='mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-primary/15 border border-primary/30'>
      <FiCheck className='h-3 w-3 text-primary-300' />
    </span>
    <span className='text-grayscale-300'>{children}</span>
  </li>
);

export default CheckItem;
export { CheckItem };
