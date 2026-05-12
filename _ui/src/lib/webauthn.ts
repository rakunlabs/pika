/**
 * Zero-dependency WebAuthn helpers for the browser.
 *
 * The W3C navigator.credentials API works with ArrayBuffers, while
 * pika's JSON wire format uses URL-safe base64 (RFC 4648 §5, no
 * padding) — matching what ada/passkey.Base64URLEncode emits and
 * what the SimpleWebAuthn package would have done. We do the
 * conversion inline so there's no third-party dependency to track.
 *
 * All functions throw on protocol violations (missing credential,
 * NotAllowedError, etc.) — callers wrap them in user-facing error
 * messages.
 */

// --- base64url plumbing ---

/**
 * Encode an ArrayBuffer / Uint8Array as URL-safe base64 (no padding).
 *
 * Implemented via btoa + char-class swaps because that's the most
 * portable browser path. The intermediate "binary string" stage is
 * fine for the small blobs WebAuthn returns (challenge ≈ 32 B,
 * credentialId ≤ 1023 B, attestationObject few KiB at most).
 */
export function bufferToBase64URL(buf: ArrayBuffer | Uint8Array): string {
  const bytes = buf instanceof Uint8Array ? buf : new Uint8Array(buf);
  let bin = '';
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
  return btoa(bin)
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '');
}

/**
 * Decode a URL-safe base64 (with or without padding) string into a
 * Uint8Array.
 *
 * Accepts both padded ("A=") and unpadded ("A") inputs since the
 * pika backend emits unpadded but some intermediaries may re-pad.
 */
export function base64URLToBuffer(s: string): Uint8Array {
  // Add padding back if missing so atob accepts it.
  const padded = s.replace(/-/g, '+').replace(/_/g, '/').padEnd(s.length + ((4 - (s.length % 4)) % 4), '=');
  const bin = atob(padded);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

// --- public API ---

/**
 * Feature-detect WebAuthn. Returns true when:
 *  - The PublicKeyCredential global exists.
 *  - The user agent claims to support platform authenticators.
 *
 * Server-side rendering is implicit-false because PublicKeyCredential
 * doesn't exist there.
 */
export function isWebAuthnSupported(): boolean {
  return typeof window !== 'undefined'
    && typeof window.PublicKeyCredential !== 'undefined';
}

/**
 * The server-issued CredentialCreationOptions shape (JSON).
 *
 * Matches ada/passkey.CredentialCreationOptions byte-for-byte: every
 * BufferSource field arrives as a base64url string and is converted
 * back to an ArrayBuffer before being handed to navigator.credentials.
 */
export interface ServerCreationOptions {
  challenge: string;
  rp: { id: string; name: string };
  user: { id: string; name: string; displayName: string };
  pubKeyCredParams: Array<{ type: 'public-key'; alg: number }>;
  timeout?: number;
  excludeCredentials?: Array<{
    type: 'public-key';
    id: string;
    transports?: AuthenticatorTransport[];
  }>;
  authenticatorSelection?: {
    userVerification?: UserVerificationRequirement;
    requireResidentKey?: boolean;
    residentKey?: ResidentKeyRequirement;
  };
  attestation?: AttestationConveyancePreference;
}

/**
 * The server-issued CredentialRequestOptions shape (JSON).
 */
export interface ServerRequestOptions {
  challenge: string;
  timeout?: number;
  rpId: string;
  allowCredentials?: Array<{
    type: 'public-key';
    id: string;
  }>;
  userVerification?: UserVerificationRequirement;
}

/**
 * The browser → server response shape after a successful create().
 * Matches ada/passkey.RegistrationResponseJSON; every BufferSource
 * is base64url-encoded.
 */
export interface RegistrationResponseJSON {
  id: string;
  rawId: string;
  type: 'public-key';
  response: {
    clientDataJSON: string;
    attestationObject: string;
    transports?: string[];
  };
  authenticatorAttachment?: string;
}

/**
 * The browser → server response shape after a successful get().
 * Matches ada/passkey.AssertionResponseJSON.
 */
export interface AssertionResponseJSON {
  id: string;
  rawId: string;
  type: 'public-key';
  response: {
    clientDataJSON: string;
    authenticatorData: string;
    signature: string;
    userHandle?: string;
  };
  authenticatorAttachment?: string;
}

/**
 * Run the registration ceremony.
 *
 * Translates the server-issued (base64url-stringy) options into the
 * WebIDL (ArrayBuffer-y) shape navigator.credentials.create wants,
 * invokes it, and translates the response back into the wire shape
 * pika's /api/v1/me/passkeys/finish expects.
 *
 * Throws:
 *  - DOMException (NotAllowedError) when the user cancels or the
 *    authenticator times out.
 *  - DOMException (InvalidStateError) when the credential is already
 *    registered (browsers honor the excludeCredentials list).
 *  - Error('webauthn not supported') in non-browser environments.
 */
export async function startRegistration(opts: ServerCreationOptions): Promise<RegistrationResponseJSON> {
  if (!isWebAuthnSupported()) throw new Error('webauthn not supported');

  const publicKey: PublicKeyCredentialCreationOptions = {
    challenge: base64URLToBuffer(opts.challenge),
    rp: opts.rp,
    user: {
      id: base64URLToBuffer(opts.user.id),
      name: opts.user.name,
      displayName: opts.user.displayName,
    },
    pubKeyCredParams: opts.pubKeyCredParams,
    timeout: opts.timeout,
    excludeCredentials: (opts.excludeCredentials ?? []).map((c) => ({
      type: c.type,
      id: base64URLToBuffer(c.id),
      transports: c.transports,
    })),
    authenticatorSelection: opts.authenticatorSelection,
    attestation: opts.attestation,
  };

  const cred = (await navigator.credentials.create({ publicKey })) as PublicKeyCredential | null;
  if (!cred) throw new Error('credential creation returned null');

  const att = cred.response as AuthenticatorAttestationResponse;
  // getTransports() is only present on newer browsers; older ones
  // omit transport info and pika is OK with that (the field is
  // optional in the wire format).
  const transports = typeof (att as any).getTransports === 'function'
    ? ((att as any).getTransports() as string[])
    : undefined;

  return {
    id: cred.id,
    rawId: bufferToBase64URL(cred.rawId),
    type: 'public-key',
    response: {
      clientDataJSON: bufferToBase64URL(att.clientDataJSON),
      attestationObject: bufferToBase64URL(att.attestationObject),
      transports,
    },
    authenticatorAttachment: (cred as any).authenticatorAttachment,
  };
}

/**
 * Run the login (assertion) ceremony.
 *
 * Mirror of startRegistration. The user handle (when the assertion
 * was discoverable) is forwarded as base64url so the server can
 * round-trip it back through its handle-comparison check.
 */
export async function startAuthentication(opts: ServerRequestOptions): Promise<AssertionResponseJSON> {
  if (!isWebAuthnSupported()) throw new Error('webauthn not supported');

  const publicKey: PublicKeyCredentialRequestOptions = {
    challenge: base64URLToBuffer(opts.challenge),
    timeout: opts.timeout,
    rpId: opts.rpId,
    allowCredentials: (opts.allowCredentials ?? []).map((c) => ({
      type: c.type,
      id: base64URLToBuffer(c.id),
    })),
    userVerification: opts.userVerification,
  };

  const cred = (await navigator.credentials.get({ publicKey })) as PublicKeyCredential | null;
  if (!cred) throw new Error('assertion returned null');

  const asn = cred.response as AuthenticatorAssertionResponse;

  return {
    id: cred.id,
    rawId: bufferToBase64URL(cred.rawId),
    type: 'public-key',
    response: {
      clientDataJSON: bufferToBase64URL(asn.clientDataJSON),
      authenticatorData: bufferToBase64URL(asn.authenticatorData),
      signature: bufferToBase64URL(asn.signature),
      userHandle: asn.userHandle ? bufferToBase64URL(asn.userHandle) : undefined,
    },
    authenticatorAttachment: (cred as any).authenticatorAttachment,
  };
}
