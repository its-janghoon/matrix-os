const Card = ({ children, className = '' }: { children: React.ReactNode; className?: string }) => (
  <div
    className={`rounded-3xl border border-white/10 bg-white/[0.03] p-8 shadow-card backdrop-blur-sm transition-all duration-300 hover:border-white/20 hover:bg-white/[0.05] ${className}`}
  >
    {children}
  </div>
);

export default Card;
export { Card };
