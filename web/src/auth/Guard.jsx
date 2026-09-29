import { Navigate, useLocation } from 'react-router-dom'
import { useAuth } from './AuthContext.jsx'

// Guard is a courtesy, not a lock.
//
// It keeps a signed-out visitor from being shown a page that would immediately
// fail, and it keeps a customer out of the panel's layout. The actual
// enforcement is the server's: the panel routes are behind Auth plus a role
// gate, and the gate looks the role up in the database rather than reading it
// from the session. Someone who edits sessionStorage to make isPanelMember
// true gets a page full of 403s, which is the correct and boring outcome.
//
// Two gates are offered. RequirePanel lets in the three panel roles — admin,
// capturista and auditor — because the college brief hands the catalogue to
// operating roles that are not administrators. RequireAdmin is the narrower
// gate the accounts and backups interfaces sit behind: nobody but an admin may
// see the whole directory or download a dump of the database.

function Guard({ children, roles }) {
  const { isAuthenticated, user, checking } = useAuth()
  const location = useLocation()

  if (checking) {
    return (
      <div className="empty-cart">
        <p className="muted">Comprobando la sesión…</p>
      </div>
    )
  }

  if (!isAuthenticated) {
    // The attempted path is passed along so the login page can send the visitor
    // back where they were headed instead of always to the home page.
    return <Navigate to="/login" replace state={{ from: location.pathname }} />
  }

  if (roles && !roles.includes(user?.role)) {
    return (
      <div className="empty-cart">
        <p className="kicker">Acceso restringido</p>
        <h2 className="auth-claim">Esta sección es<br />solo para el equipo.</h2>
        <p className="muted">Si crees que se trata de un error, contacta con la administración.</p>
      </div>
    )
  }

  return children
}

export function RequireAuth({ children }) {
  return <Guard>{children}</Guard>
}

export function RequirePanel({ children }) {
  return (
    <Guard roles={['admin', 'capturista', 'auditor']}>{children}</Guard>
  )
}

export function RequireAdmin({ children }) {
  return <Guard roles={['admin']}>{children}</Guard>
}
