import { motion, useReducedMotion } from "motion/react";

const particles = Array.from({ length: 20 }, (_, index) => ({
  id: index,
  left: `${(index * 37) % 100}%`,
  delay: (index % 7) * 0.7,
  duration: 12 + (index % 6),
  size: 1 + (index % 3)
}));

function StaticBackdrop() {
  return (
    <div aria-hidden className="pointer-events-none fixed inset-0 -z-10 overflow-hidden bg-[#020617]">
      <div className="absolute -left-24 top-[-8rem] h-80 w-80 rounded-full bg-[#ff8c6b]/20 blur-[120px] mix-blend-screen" />
      <div className="absolute bottom-[-9rem] right-[-9rem] h-[28rem] w-[28rem] rounded-full bg-[#0ea5e9]/20 blur-[140px] mix-blend-screen" />
      <div className="absolute left-1/2 top-1/3 h-72 w-72 -translate-x-1/2 rounded-full bg-[#2dd4bf]/14 blur-[100px] mix-blend-screen" />
      <div className="bg-noise absolute inset-0 opacity-[0.15] mix-blend-color-dodge" />
      <div className="absolute left-8 top-[28%] h-28 w-28 rotate-12 border border-[#2dd4bf]/40 bg-[#2dd4bf]/[0.04] [clip-path:polygon(50%_0%,100%_38%,82%_100%,18%_100%,0%_38%)]" />
      <div className="absolute right-8 top-[22%] h-24 w-36 skew-x-[-18deg] border border-[#ff8c6b]/40 bg-[#ff8c6b]/[0.04]" />
    </div>
  );
}

export function AppBackground() {
  const reduceMotion = useReducedMotion();
  if (reduceMotion) return <StaticBackdrop />;

  return (
    <div aria-hidden className="pointer-events-none fixed inset-0 -z-10 overflow-hidden bg-[#020617]">
      <motion.div
        className="absolute -left-24 top-[-8rem] h-80 w-80 rounded-full bg-[#ff8c6b]/20 blur-[120px] mix-blend-screen"
        animate={{ x: [0, 24, 0], y: [0, 18, 0], opacity: [0.18, 0.28, 0.18] }}
        transition={{ duration: 18, repeat: Infinity, ease: "easeInOut" }}
      />
      <motion.div
        className="absolute bottom-[-9rem] right-[-9rem] h-[28rem] w-[28rem] rounded-full bg-[#0ea5e9]/20 blur-[140px] mix-blend-screen"
        animate={{ x: [0, -28, 0], y: [0, -22, 0], opacity: [0.16, 0.26, 0.16] }}
        transition={{ duration: 22, repeat: Infinity, ease: "easeInOut" }}
      />
      <motion.div
        className="absolute left-1/2 top-1/3 h-72 w-72 -translate-x-1/2 rounded-full bg-[#2dd4bf]/14 blur-[100px] mix-blend-screen"
        animate={{ scale: [1, 1.18, 1], opacity: [0.12, 0.22, 0.12] }}
        transition={{ duration: 20, repeat: Infinity, ease: "easeInOut" }}
      />
      <div className="bg-noise absolute inset-0 opacity-[0.15] mix-blend-color-dodge" />
      {particles.map((particle) => (
        <motion.span
          key={particle.id}
          className="absolute bottom-[-10px] rounded-full bg-white/70"
          style={{ left: particle.left, width: particle.size, height: particle.size }}
          animate={{ y: ["0vh", "-110vh"], opacity: [0, 0.55, 0] }}
          transition={{ duration: particle.duration, delay: particle.delay, repeat: Infinity, ease: "linear" }}
        />
      ))}
      <motion.div
        className="absolute left-8 top-[28%] h-28 w-28 rotate-12 border border-[#2dd4bf]/40 bg-gradient-to-br from-[#2dd4bf]/15 to-transparent [clip-path:polygon(50%_0%,100%_38%,82%_100%,18%_100%,0%_38%)]"
        animate={{ y: [0, -18, 0], rotate: [12, 18, 12] }}
        transition={{ duration: 16, repeat: Infinity, ease: "easeInOut" }}
      />
      <motion.div
        className="absolute right-8 top-[22%] h-24 w-36 skew-x-[-18deg] border border-[#ff8c6b]/40 bg-gradient-to-br from-[#ff8c6b]/15 to-transparent"
        animate={{ y: [0, 16, 0], x: [0, -10, 0] }}
        transition={{ duration: 19, repeat: Infinity, ease: "easeInOut" }}
      />
    </div>
  );
}
