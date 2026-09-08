// Same base64url/ArrayBuffer boundary as Pika; no platform-biometric requirement.
export function isWebAuthnSupported(): boolean {
  return typeof window !== 'undefined' && window.isSecureContext === true
    && typeof window.PublicKeyCredential === 'function'
    && typeof navigator !== 'undefined'
    && typeof navigator.credentials?.create === 'function'
    && typeof navigator.credentials?.get === 'function';
}

export function bufferToBase64URL(buffer: ArrayBuffer | Uint8Array): string {
  const bytes = buffer instanceof Uint8Array ? buffer : new Uint8Array(buffer);
  let binary = '';
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}

export function base64URLToBuffer(value: string): ArrayBuffer {
  if (!/^[A-Za-z0-9_-]*$/.test(value) || value.length % 4 === 1) throw new Error('Invalid base64url');
  const binary = atob(value.replace(/-/g, '+').replace(/_/g, '/'));
  return Uint8Array.from(binary, char => char.charCodeAt(0)).buffer;
}

export function creationOptions(options: PublicKeyCredentialCreationOptionsJSON): PublicKeyCredentialCreationOptions {
  if (typeof window.PublicKeyCredential.parseCreationOptionsFromJSON === 'function') {
    return window.PublicKeyCredential.parseCreationOptionsFromJSON(options);
  }
  return {
    ...options,
    challenge: base64URLToBuffer(options.challenge),
    user: { ...options.user, id: base64URLToBuffer(options.user.id) },
    excludeCredentials: options.excludeCredentials?.map(item => ({ ...item, type: 'public-key', id: base64URLToBuffer(item.id), transports: item.transports as AuthenticatorTransport[] | undefined })),
  } as PublicKeyCredentialCreationOptions;
}

export function requestOptions(options: PublicKeyCredentialRequestOptionsJSON): PublicKeyCredentialRequestOptions {
  if (typeof window.PublicKeyCredential.parseRequestOptionsFromJSON === 'function') {
    return window.PublicKeyCredential.parseRequestOptionsFromJSON(options);
  }
  return {
    ...options,
    challenge: base64URLToBuffer(options.challenge),
    allowCredentials: options.allowCredentials?.map(item => ({ ...item, type: 'public-key', id: base64URLToBuffer(item.id), transports: item.transports as AuthenticatorTransport[] | undefined })),
  } as PublicKeyCredentialRequestOptions;
}

export interface RawCredential {
  id: string;
  rawId: string;
  type: 'public-key';
  authenticatorAttachment?: string;
  response: {
    clientDataJSON: string;
    attestationObject?: string;
    transports?: string[];
    authenticatorData?: string;
    signature?: string;
    userHandle?: string | null;
  };
}

export function serializeCredential(credential: PublicKeyCredential): RawCredential {
  const response = credential.response;
  return {
    id: credential.id,
    rawId: bufferToBase64URL(credential.rawId),
    type: 'public-key',
    ...(credential.authenticatorAttachment ? { authenticatorAttachment: credential.authenticatorAttachment } : {}),
    response: 'attestationObject' in response ? {
      clientDataJSON: bufferToBase64URL(response.clientDataJSON),
      attestationObject: bufferToBase64URL((response as AuthenticatorAttestationResponse).attestationObject),
      ...('getTransports' in response && typeof response.getTransports === 'function' ? { transports: response.getTransports() } : {}),
    } : {
      clientDataJSON: bufferToBase64URL(response.clientDataJSON),
      authenticatorData: bufferToBase64URL((response as AuthenticatorAssertionResponse).authenticatorData),
      signature: bufferToBase64URL((response as AuthenticatorAssertionResponse).signature),
      userHandle: (response as AuthenticatorAssertionResponse).userHandle == null ? null : bufferToBase64URL((response as AuthenticatorAssertionResponse).userHandle!),
    },
  };
}

export function isPasskeyCancellation(error: unknown): boolean {
  return error instanceof Error && (error.name === 'AbortError' || error.name === 'NotAllowedError');
}

export async function startRegistration(options: PublicKeyCredentialCreationOptionsJSON, signal: AbortSignal): Promise<RawCredential | null> {
  signal.throwIfAborted();
  const credential = await navigator.credentials.create({ publicKey: creationOptions(options), signal });
  signal.throwIfAborted();
  return credential ? serializeCredential(credential as PublicKeyCredential) : null;
}

export async function startAuthentication(options: PublicKeyCredentialRequestOptionsJSON, signal: AbortSignal): Promise<RawCredential | null> {
  signal.throwIfAborted();
  const credential = await navigator.credentials.get({ publicKey: requestOptions(options), signal });
  signal.throwIfAborted();
  return credential ? serializeCredential(credential as PublicKeyCredential) : null;
}
