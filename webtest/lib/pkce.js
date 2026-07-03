// pkce.js — PKCE verifier/challenge helpers (RFC 7636).

function b64url(bytes) {
  let bin = '';
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

// verifier returns a fresh high-entropy code_verifier (base64url, 43 chars).
export function verifier() {
  const arr = new Uint8Array(32);
  crypto.getRandomValues(arr);
  return b64url(arr);
}

// challengeS256 returns base64url(SHA-256(verifier)), the S256 code_challenge.
export async function challengeS256(v) {
  const data = new TextEncoder().encode(v);
  const digest = await crypto.subtle.digest('SHA-256', data);
  return b64url(new Uint8Array(digest));
}

export default { verifier, challengeS256 };
