// Section wrapper with a consistent container + vertical rhythm.
const Section = ({
  children,
  className = '',
}: {
  children: React.ReactNode;
  className?: string;
}) => (
  <section className={`relative py-24 sm:py-28 ${className}`}>
    <div className='mx-auto max-w-7xl px-4 sm:px-6 lg:px-8'>{children}</div>
  </section>
);

export default Section;
export { Section };
