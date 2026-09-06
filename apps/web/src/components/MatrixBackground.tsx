'use client';

import { Canvas, useFrame } from '@react-three/fiber';
import { useMemo, useRef } from 'react';
import * as THREE from 'three';

const MatrixRain = () => {
  const groupRef = useRef<THREE.Group>(null);

  // Create matrix characters in a spherical pattern
  const characters = useMemo(() => {
    const radius = 40;
    const count = 2500;

    return Array.from({ length: count }, () => {
      // Spherical coordinates
      const theta = Math.random() * Math.PI * 2;
      const phi = Math.acos(2 * Math.random() - 1);

      // Convert to Cartesian coordinates
      const x = radius * Math.sin(phi) * Math.cos(theta);
      const y = radius * Math.sin(phi) * Math.sin(theta);
      const z = radius * Math.cos(phi);

      const size = 0.08 + Math.random() * 0.05;
      const color = new THREE.Color(0, Math.random() * 0.3 + 0.2, 0);
      const opacity = Math.random() * 0.3 + 0.2;
      return { x, y, z, size, color, opacity };
    });
  }, []);

  useFrame((state, delta) => {
    if (groupRef.current) {
      // Very slow, fluid rotation
      groupRef.current.rotation.y += delta * 0.05;
      groupRef.current.rotation.x += delta * 0.02;

      // Randomly change some characters' properties
      groupRef.current.children.forEach((child, index) => {
        if (child instanceof THREE.Mesh && Math.random() < 0.01) {
          const char = characters[index];
          characters[index].opacity = Math.random() * 0.3 + 0.2;
          (child.material as THREE.MeshStandardMaterial).opacity = char.opacity;
        }
      });
    }
  });

  return (
    <group ref={groupRef}>
      {characters.map((char, index) => (
        <mesh key={index} position={[char.x, char.y, char.z]}>
          <sphereGeometry args={[char.size, 12, 12]} />
          <meshStandardMaterial
            color={char.color}
            emissive={char.color}
            emissiveIntensity={1.5}
            transparent
            opacity={char.opacity}
            roughness={0.3}
            metalness={0.6}
          />
        </mesh>
      ))}
    </group>
  );
};

export default function MatrixBackground() {
  return (
    <div className='fixed inset-0 z-0 h-screen w-screen'>
      <Canvas camera={{ position: [0, 0, 60], fov: 75 }}>
        <ambientLight intensity={0.8} />
        <pointLight position={[10, 10, 10]} intensity={1.5} />
        <MatrixRain />
      </Canvas>
    </div>
  );
}
