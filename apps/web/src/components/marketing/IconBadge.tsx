const IconBadge = ({
  icon: Icon,
  tone = 'primary',
}: {
  icon: React.ComponentType<{ className?: string }>;
  tone?: 'primary' | 'secondary' | 'accent';
}) => {
  const tones = {
    primary: 'bg-primary/10 border-primary/25 text-primary-300',
    secondary: 'bg-secondary/10 border-secondary/25 text-secondary-300',
    accent: 'bg-accent-300/10 border-accent-300/25 text-accent-300',
  } as const;
  return (
    <div className={`flex h-12 w-12 items-center justify-center rounded-2xl border ${tones[tone]}`}>
      <Icon className='h-5 w-5' />
    </div>
  );
};

export default IconBadge;
export { IconBadge };
