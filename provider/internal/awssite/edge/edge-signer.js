// Anvil edge signer — Lambda@Edge (origin-request, includeBody).
//
// A Lambda Function URL locked with AuthType AWS_IAM + CloudFront OAC rejects
// requests with a body unless they carry x-amz-content-sha256 (the SHA-256 of
// the body). Browsers don't send it, so this function adds it at the edge,
// before CloudFront's OAC signs the request — the app and its pages are
// untouched. This is the approach AWS documents for POST/PUT to an
// IAM-protected Function URL behind CloudFront:
// https://docs.aws.amazon.com/AmazonCloudFront/latest/DeveloperGuide/private-content-restricting-access-to-lambda.html
//
// It also percent-encodes query parameter names: the locked Function URL
// rejects names containing raw special characters (InvalidQueryStringException),
// which breaks SvelteKit form actions (`?/save`) and similar URLs.
'use strict';

const crypto = require('crypto');

const METHODS_WITH_BODY = ['POST', 'PUT', 'PATCH'];

// Characters left as-is in a query parameter name. Everything else — including
// "~" and "+", which the Function URL also rejects raw — is percent-encoded.
const SAFE_NAME_BYTE = /[A-Za-z0-9\-_.]/;

// Re-encode a query parameter name so it only contains safe characters. The
// app decodes it back to the same name: "%2Fsave" is read as "/save". A "+"
// means a space in query strings, so it becomes "%20" to keep that meaning.
function encodeName(name) {
  let decoded;
  try {
    decoded = decodeURIComponent(name.replace(/\+/g, ' '));
  } catch (e) {
    return name; // malformed percent-encoding: leave it for the app to reject
  }
  let out = '';
  for (const byte of Buffer.from(decoded, 'utf8')) {
    const ch = String.fromCharCode(byte);
    out += SAFE_NAME_BYTE.test(ch) ? ch : '%' + byte.toString(16).toUpperCase().padStart(2, '0');
  }
  return out;
}

// Encode the name of every parameter; values are accepted as-is.
function normaliseQueryString(qs) {
  return qs
    .split('&')
    .map((part) => {
      const eq = part.indexOf('=');
      return eq === -1 ? encodeName(part) : encodeName(part.slice(0, eq)) + part.slice(eq);
    })
    .join('&');
}

exports.handler = async (event) => {
  const request = event.Records[0].cf.request;

  if (request.querystring) {
    request.querystring = normaliseQueryString(request.querystring);
  }

  if (!METHODS_WITH_BODY.includes(request.method)) {
    return request;
  }

  // Lambda@Edge only receives the first 1 MB of the body; a hash of a
  // truncated body would never match, so reject clearly instead.
  if (request.body && request.body.inputTruncated) {
    return {
      status: '413',
      statusDescription: 'Payload Too Large',
      headers: {
        'content-type': [{ key: 'Content-Type', value: 'application/json' }],
      },
      body: JSON.stringify({
        error: 'Request body exceeds the 1 MB Lambda@Edge limit. Upload large files directly to S3 with a presigned URL.',
      }),
    };
  }

  // Hash the raw bytes — never round-trip through a string.
  const body = request.body && request.body.data
    ? Buffer.from(request.body.data, request.body.encoding === 'base64' ? 'base64' : 'utf8')
    : Buffer.alloc(0);

  request.headers['x-amz-content-sha256'] = [{
    key: 'x-amz-content-sha256',
    value: crypto.createHash('sha256').update(body).digest('hex'),
  }];

  return request;
};

exports.normaliseQueryString = normaliseQueryString;
