// jose.js — craft dummy-signature compact JWTs.
//
// The mock server parses assertion / subject_token JWTs UNVERIFIED, so the
// console only needs decodable tokens whose signature never verifies. Every
// token produced here is base64url(header).base64url(payload).base64url(
// 'dummy-signature').

function b64urlFromString(str) {
  const bytes = new TextEncoder().encode(str);
  let bin = '';
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

function randomId() {
  const arr = new Uint8Array(16);
  crypto.getRandomValues(arr);
  return Array.from(arr, (b) => b.toString(16).padStart(2, '0')).join('');
}

// craft assembles a compact JWT from a header and payload. The signature segment
// is the fixed base64url of the ASCII string 'dummy-signature': the token
// decodes but never verifies.
export function craft({ header = { alg: 'RS256', typ: 'JWT' }, payload = {} } = {}) {
  const h = b64urlFromString(JSON.stringify(header));
  const p = b64urlFromString(JSON.stringify(payload));
  const sig = b64urlFromString('dummy-signature');
  return `${h}.${p}.${sig}`;
}

// craftPrivateKeyJwt builds a client_assertion shaped to satisfy the server's
// structural private_key_jwt checks (iss==sub==clientId, single aud, exp-iat
// within bounds). iatOffset shifts iat/exp to probe the exp-iat window; lifetime
// sets exp-iat. The signature is never verified.
export function craftPrivateKeyJwt({ clientId, aud, iatOffset = 0, lifetime = 60, iss, sub } = {}) {
  const iat = Math.floor(Date.now() / 1000) + iatOffset;
  const payload = {
    iss: iss || clientId,
    sub: sub || clientId,
    aud,
    iat,
    exp: iat + lifetime,
    jti: randomId(),
  };
  return craft({ header: { alg: 'RS256', typ: 'JWT' }, payload });
}

export default { craft, craftPrivateKeyJwt };
