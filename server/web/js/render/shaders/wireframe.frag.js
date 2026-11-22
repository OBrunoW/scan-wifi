export const wireframeFragmentShader = `
uniform vec3 uStops[5];
uniform float uDensity;
uniform float uThickness;
uniform float uAlpha;
uniform float uBackOpacity;
uniform float uFlicker;
uniform float uTime;
uniform float uDepthRef;
uniform float uDepthRange;

varying vec2 vUv;
varying vec2 vGrid;
varying vec3 vNormalView;
varying vec3 vViewPosition;
varying float vNoise;

vec3 colormap(float t) {
  float scaled = clamp(t, 0.0, 1.0) * 4.0;
  int index = int(min(floor(scaled), 3.0));
  float k = scaled - float(index);
  return mix(uStops[index], uStops[index + 1], k);
}

void main() {
  vec2 cells = max(floor(vGrid * uDensity + 0.5), vec2(1.0));
  vec2 cell = vUv * cells;
  vec2 width = max(fwidth(cell), vec2(0.0001));
  vec2 distanceToLine = abs(fract(cell - 0.5) - 0.5) / width;
  float nearest = min(distanceToLine.x, distanceToLine.y);
  float line = clamp(uThickness * 0.5 + 0.5 - nearest, 0.0, 1.0);
  if (line < 0.01) discard;

  vec3 normal = normalize(vNormalView);
  vec3 viewDirection = normalize(-vViewPosition);
  float facing = abs(dot(normal, viewDirection));
  float depthT = clamp(0.5 - (-vViewPosition.z - uDepthRef) / uDepthRange, 0.0, 1.0);
  float t = clamp(0.65 * facing + 0.35 * depthT, 0.0, 1.0);

  float shimmer = 1.0 - uFlicker * 0.5 * (0.5 + 0.5 * sin(uTime * 6.0 + vNoise * 6.2831853));
  float side = gl_FrontFacing ? 1.0 : uBackOpacity;

  gl_FragColor = vec4(colormap(t) * shimmer, line * side * uAlpha);
  #include <colorspace_fragment>
}
`;
