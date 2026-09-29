// The session lives in an httpOnly cookie, not in localStorage.
//
// A token in localStorage is readable by any script that runs on the page, so a
// single XSS is a complete account takeover, and it outlives the tab and the
// session. The server sets the cookie; this module never sees the token, which
// is the point. The CSRF cookie is the deliberate exception: it must be
// readable so it can be echoed in a header a cross-origin form cannot set.

const CSRF_COOKIE = 'dvbs_csrf'
const CSRF_HEADER = 'X-CSRF-Token'

// The API is mounted under /api everywhere: in production Netlify proxies that
// prefix to the Render backend, and in development both the Vite proxy and the
// nginx reverse proxy route it the same way. Everything else is SPA territory,
// which is what removes the old /admin collision between a page route and an
// API path.
const API_BASE = '/api'

// apiPath resolves a path that came from the server, like the /uploads/... url
// of an uploaded review image, to a full request path. It is also the base for
// the handful of requests that cannot go through the JSON helper because they
// send or receive non-JSON bodies.
export function apiPath(p) {
  if (!p) return p
  if (/^https?:/.test(p) || p.startsWith(API_BASE)) return p
  if (p.startsWith('/')) return API_BASE + p
  return `${API_BASE}/${p}`
}

function readCookie(name) {
  for (const part of document.cookie.split(';')) {
    const [key, ...rest] = part.trim().split('=')
    if (key === name) return decodeURIComponent(rest.join('='))
  }
  return null
}

// The user record is a display convenience only, kept in sessionStorage so it
// disappears when the tab closes. It is never trusted: the server decides
// permissions from the cookie and the database.
const USER_KEY = 'user'

export function getUser() {
  try {
    return JSON.parse(sessionStorage.getItem(USER_KEY) || 'null')
  } catch {
    return null
  }
}

export function setUser(user) {
  sessionStorage.setItem(USER_KEY, JSON.stringify(user))
}

export function clearSession() {
  sessionStorage.removeItem(USER_KEY)
}

// ensureCSRF obtains a token if we do not have one.
//
// The first state-changing call of a fresh visit would otherwise be rejected:
// there is no cookie to echo yet. The server can mint one on demand at
// /auth/csrf, so a first-time visitor is not stuck.
let csrfPromise = null

export function ensureCSRF() {
  if (readCookie(CSRF_COOKIE)) return Promise.resolve()
  if (!csrfPromise) {
    csrfPromise = fetch(apiPath('/auth/csrf'), { credentials: 'include' })
      .then(() => {
        if (!readCookie(CSRF_COOKIE)) {
          throw new Error('the server did not return a csrf token')
        }
      })
      .finally(() => {
        csrfPromise = null
      })
  }
  return csrfPromise
}

// forceMintCSRF is the recovery step for a rejected state-changing request.
//
// The double-submit check only fails when the cookie the browser attached and
// the header we echoed disagree, which happens when the browser holds a csrf
// cookie that is stale, was wiped by a browser restart (it is a session cookie,
// while the session cookie itself survives), or got duplicated. The bootstrap
// endpoint re-mints and overwrites in a single Set-Cookie, so after this call
// the browser owns exactly one value and the retried request has nothing left
// to disagree with. An attacker gains nothing: any page could already have the
// client call a GET; what it cannot do is read our answer, and the retried POST
// needs the value we now hold.
export async function forceMintCSRF() {
  const res = await fetch(apiPath('/auth/csrf'), { credentials: 'include' })
  return res.ok ? readCookie(CSRF_COOKIE) : null
}

const SAFE_METHODS = new Set(['GET', 'HEAD', 'OPTIONS'])

// readCSRF is exported for the one request that cannot use the JSON helper,
// because it sends multipart form data. It is a reader, not a setter: the server
// decides the value.
export { readCookie as readCSRF, CSRF_HEADER }

// unsafeFetch is for requests that cannot go through the JSON helper because
// the body or the response is not JSON (multipart upload, file download). It
// attaches the CSRF token the same way request() does and retries once after a
// fresh token mint if the server rejected the pair.
export async function unsafeFetch(path, init = {}) {
  const headers = new Headers(init.headers)
  for (let attempt = 0; attempt < 2; attempt++) {
    await ensureCSRF()
    const token = readCookie(CSRF_COOKIE)
    if (token) headers.set(CSRF_HEADER, token)
    const res = await fetch(apiPath(path), { ...init, headers, credentials: 'include' })
    if (res.status === 403 && attempt === 0) {
      await forceMintCSRF()
      continue
    }
    return res
  }
}

async function request(method, path, body) {
  for (let attempt = 0; attempt < 2; attempt++) {
    const headers = {}
    if (body !== undefined) headers['Content-Type'] = 'application/json'

    if (!SAFE_METHODS.has(method)) {
      await ensureCSRF()
      const token = readCookie(CSRF_COOKIE)
      if (token) headers[CSRF_HEADER] = token
    }

    const res = await fetch(apiPath(path), {
      method,
      headers,
      // Without this the browser never sends the session cookie at all.
      credentials: 'include',
      body: body !== undefined ? JSON.stringify(body) : undefined,
    })

    // A 403 here is a desync between cookie and header, not anything the
    // request did wrong (nothing was written), so it is safe to re-mint the
    // token and retry once.
    if (res.status === 403 && attempt === 0 && !SAFE_METHODS.has(method)) {
      await forceMintCSRF()
      continue
    }

    const data = await res.json().catch(() => null)

    // A 401 means the session is gone or was revoked, most often because the
    // password was reset. Dropping the cached user stops the UI from continuing to
    // render an authenticated shell that no longer works.
    if (res.status === 401) {
      clearSession()
      window.dispatchEvent(new CustomEvent('auth:expired'))
    }

    return { status: res.status, ok: res.ok, data }
  }
}

export const api = {
  get: (path) => request('GET', path),
  post: (path, body) => request('POST', path, body),
  put: (path, body) => request('PUT', path, body),
  del: (path) => request('DELETE', path),
  logout: () => request('POST', '/auth/logout', {}),
}
