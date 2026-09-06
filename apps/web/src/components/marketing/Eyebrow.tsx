const Eyebrow = ({
  children,
  tone = 'primary',
}: {
  children: React.ReactNode;
  tone?: 'primary' | 'secondary' | 'accent';
}) => {
  const tones = {
    primary: 'bg-primary/10 text-primary-300 border-primary/25',
    secondary: 'bg-secondary/10 text-secondary-300 border-secondary/25',
    accent: 'bg-accent-300/10 text-accent-300 border-accent-300/25',
  } as const;
  return (
    <span
      className={`inline-flex items-center gap-2 rounded-full border px-3 py-1 text-xs font-semibold uppercase tracking-[0.12em] ${tones[tone]}`}
    >
      {children}
    </span>
  );
};

export default Eyebrow;
export { Eyebrow };
