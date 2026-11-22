export const wireframeVertexShader = `
attribute vec2 aGrid;

varying vec2 vUv;
varying vec2 vGrid;
varying vec3 vNormalView;
varying vec3 vViewPosition;
varying float vNoise;

void main() {
  vUv = uv;
  vGrid = aGrid;
  vec4 viewPosition = modelViewMatrix * vec4(position, 1.0);
  vViewPosition = viewPosition.xyz;
  vNormalView = normalize(normalMatrix * normal);
  vNoise = fract(sin(dot(position, vec3(12.9898, 78.233, 37.719))) * 43758.5453);
  gl_Position = projectionMatrix * viewPosition;
}
`;
