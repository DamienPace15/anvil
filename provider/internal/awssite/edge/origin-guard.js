// Anvil origin guard — CloudFront Function (viewer-request, cloudfront-js-2.0).
//
// Origin protection: only requests carrying the secret x-origin-secret header,
// added by the CDN/proxy in front of CloudFront, are let through. Everything
// else gets a 403 before reaching the cache or the origin.
//
// The secret lives in a CloudFront KeyValueStore, not in this code, so reading
// the function doesn't reveal it. The header is removed after the check so the
// app never receives (and can't accidentally log) the secret.
import cf from 'cloudfront';

const kvs = cf.kvs('__KVS_ID__');
const HEADER = 'x-origin-secret';

// KeyValueStore keys whose values are accepted. Rotation without downtime adds
// a second key here (accept old and new while the proxy is updated).
const SECRET_KEYS = ['origin-secret'];

// Constant-time comparison so response timing doesn't reveal how much of the
// secret matched.
function safeEqual(a, b) {
  if (typeof a !== 'string' || typeof b !== 'string' || a.length !== b.length) {
    return false;
  }
  let diff = 0;
  for (let i = 0; i < a.length; i++) {
    diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  }
  return diff === 0;
}

async function handler(event) {
  const request = event.request;
  const provided = request.headers[HEADER];

  if (provided) {
    for (let i = 0; i < SECRET_KEYS.length; i++) {
      let expected;
      try {
        expected = await kvs.get(SECRET_KEYS[i]);
      } catch (e) {
        continue; // key not present
      }
      if (safeEqual(provided.value, expected)) {
        delete request.headers[HEADER];
        return request;
      }
    }
  }

  return { statusCode: 403, statusDescription: 'Forbidden' };
}
