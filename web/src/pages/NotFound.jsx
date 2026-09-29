import { Link, useLocation } from 'react-router-dom'

export default function NotFound() {
  const location = useLocation()

  return (
    <div className="empty-cart">
      <p className="kicker">Error 404</p>
      <h2 className="auth-claim">Esta página<br />no existe.</h2>
      <p className="muted">
        No hay nada en <code>{location.pathname}</code>. Puede que el enlace sea
        antiguo o que la dirección tenga una errata.
      </p>
      <Link to="/catalogo" className="btn btn-solid">Ir al catálogo</Link>
    </div>
  )
}
