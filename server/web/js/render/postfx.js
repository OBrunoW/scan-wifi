import * as THREE from 'three';
import { EffectComposer } from 'three/addons/postprocessing/EffectComposer.js';
import { RenderPass } from 'three/addons/postprocessing/RenderPass.js';
import { UnrealBloomPass } from 'three/addons/postprocessing/UnrealBloomPass.js';
import { OutputPass } from 'three/addons/postprocessing/OutputPass.js';

export const BLOOM_RADIUS = 0.4;
export const BLOOM_THRESHOLD = 0.1;

export function createPostfx({ renderer, scene, camera, width, height }) {
  const composer = new EffectComposer(renderer);
  composer.setPixelRatio(renderer.getPixelRatio());
  composer.setSize(width, height);
  composer.addPass(new RenderPass(scene, camera));
  const bloom = new UnrealBloomPass(new THREE.Vector2(width, height), 0.6, BLOOM_RADIUS, BLOOM_THRESHOLD);
  composer.addPass(bloom);
  composer.addPass(new OutputPass());

  return {
    setSize(nextWidth, nextHeight) {
      composer.setSize(nextWidth, nextHeight);
    },
    setStrength(strength) {
      bloom.strength = strength;
    },
    render() {
      composer.render();
    },
    dispose() {
      composer.dispose();
    },
  };
}
