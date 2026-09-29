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

const SAFE_METHODS = new Set(['GET', 'HEAD', 'OPTIONS'])

// readCSRF is exported for the one request that cannot use the JSON helper,
// because it sends multipart form data. It is a reader, not a setter: the server
// decides the value.
export { readCookie as readCSRF, CSRF_HEADER }

async function request(method, path, body) {
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

export const api = {
  get: (path) => request('GET', path),
  post: (path, body) => request('POST', path, body),
  put: (path, body) => request('PUT', path, body),
  del: (path) => request('DELETE', path),
  logout: () => request('POST', '/auth/logout', {}),
}
