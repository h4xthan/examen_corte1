import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import { api, clearSession, getUser, setUser } from '../api/client.js'

// AuthContext holds the one thing every page needs to know: who, if anyone, is
// signed in.
//
// The reload() calls that used to follow login and logout are gone. They were
// there because the user record lived in a module-level variable that no
// component subscribed to, so the only way to re-render the topbar was to throw
// away the page. A context is the actual fix, and it also gives the route
// guards a single source to ask.
//
// What it is *not* is an authority. The record in here came from a login
// response and can be edited by anyone with devtools open. Every permission in
// this app is decided by the server, which reads the session cookie and looks the
// role up in the database on each request. The guards below are a courtesy that
// saves a pointless round trip and a blank page; they are not a lock.
const AuthContext = createContext(null)

export function AuthProvider({ children }) {
  const [user, setUserState] = useState(() => getUser())
  const [checking, setChecking] = useState(!getUser())

  const applyUser = useCallback((next) => {
    if (next) {
      setUser(next)
    } else {
      clearSession()
    }
    setUserState(next)
  }, [])

  // A tab that starts with no cached user may still hold a valid session cookie,
  // because the cookie outlives the tab. Asking the server is the only way to
  // know, and it is what stops a reload from logging the visitor out.
  useEffect(() => {
    if (getUser()) return undefined
    let cancelled = false

    api.get('/users/me')
      .then((res) => {
        if (cancelled) return
        if (res.ok && res.data) {
          setUser(res.data)
          setUserState(res.data)
        }
      })
      .catch(() => {})
      .finally(() => {
        if (!cancelled) setChecking(false)
      })

    return () => {
      cancelled = true
    }
  }, [])

  // api dispatches this when any request comes back 401, which is how a revoked
  // or expired session takes effect across the whole app at once.
  useEffect(() => {
    const onExpired = () => applyUser(null)
    window.addEventListener('auth:expired', onExpired)
    return () => window.removeEventListener('auth:expired', onExpired)
  }, [applyUser])

  const value = useMemo(
    () => ({
      user,
      // True while the first /users/me is in flight. A guard renders a spinner
      // instead of bouncing the visitor to /login for a fraction of a second.
      checking,
      isAuthenticated: Boolean(user),
      // Display convenience for navigation. A forged value in sessionStorage
      // only changes what this app offers to click.
      isAdmin: user?.role === 'admin',
      // A capturista may manage the catalogue, an auditor may only read the
      // whole shop, and the admin no longer captures books; all three are
      // members of the panel, which is the gate RequirePanel checks.
      isPanelMember: user?.role === 'admin' || user?.role === 'capturista' || user?.role === 'auditor',
      isCatalogWriter: user?.role === 'capturista',
      setUser: applyUser,
    }),
    [user, checking, applyUser],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used inside an AuthProvider')
  return ctx
}
