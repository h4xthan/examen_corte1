import { useEffect, useState } from 'react'
import { Link, NavLink, Route, Routes, useNavigate } from 'react-router-dom'
import { api } from './api/client.js'
import { getCart, cartCount as countUnits } from './lib/cart.js'
import { useAuth } from './auth/AuthContext.jsx'
import { RequirePanel, RequireAuth } from './auth/Guard.jsx'
import Home from './pages/Home.jsx'
import Catalog from './pages/Catalog.jsx'
import BookDetail from './pages/BookDetail.jsx'
import Login from './pages/Login.jsx'
import Cart from './pages/Cart.jsx'
import Profile from './pages/Profile.jsx'
import Admin from './pages/Admin.jsx'
import NotFound from './pages/NotFound.jsx'

const NAV_PRESETS = [
  { label: 'Inicio', to: '/' },
  { label: 'Categorías', to: '/catalogo' },
  { label: 'Destacados', to: '/catalogo?orden=destacados' },
  { label: 'Populares', to: '/catalogo?orden=populares' },
  // There is no "Ofertas" entry. It filtered on a 10% markdown the browser
  // invented from the book's id, which the server never applied, so the shelf it
  // pointed at was promising a price nobody would be charged. Coupons are real
  // and are entered at checkout, where the server can price them.
  { label: 'Novedades', to: '/catalogo?orden=nuevos' },
]

export default function App() {
  const { user, isAdmin, setUser } = useAuth()
  const navigate = useNavigate()
  const [cartCount, setCartCount] = useState(() => countUnits(getCart()))
  const [query, setQuery] = useState('')

  useEffect(() => {
    const onUpdate = () => setCartCount(countUnits(getCart()))
    window.addEventListener('cart-changed', onUpdate)
    return () => window.removeEventListener('cart-changed', onUpdate)
  }, [])

  async function logout() {
    // The server has to be told: dropping the cached user alone leaves the
    // session cookie in the browser, still valid and still attached to every
    // later request. The context then re-renders, which is what the
    // window.location.reload() this replaced used to be doing by brute force.
    await api.logout().catch(() => {})
    setUser(null)
    navigate('/')
  }

  function search(e) {
    e.preventDefault()
    navigate(`/catalogo?q=${encodeURIComponent(query.trim())}`)
  }

  return (
    <div className="app">
      <div className="promo-bar">
        <Link to="/cart">Cupón de 20% de descuento: <b>DVBS20</b></Link>
      </div>
      <header className="topbar">
        <div className="topbar-inner">
          <Link to="/" className="logo">
            DVBS<span className="logo-dot">.</span>
            <span className="logo-sub">libros & relatos</span>
          </Link>

          <nav className="mainnav">
            {NAV_PRESETS.map((item) => (
              <NavLink key={item.label} to={item.to} end={item.to === '/'}>
                {item.label}
              </NavLink>
            ))}
          </nav>

          <form className="search" onSubmit={search}>
            <input
              placeholder="Buscar título o autor…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </form>

          <div className="topbar-actions">
            {user ? (
              <>
                <Link to="/profile" className="action-link">
                  {user.first_name || 'Cuenta'}
                </Link>
                {/* The admin link is a shortcut, not a permission. The route
                    behind it re-checks, and the server checks again on every
                    call. */}
                {isAdmin && (
                  <Link to="/admin" className="action-link">Administración</Link>
                )}
                <button className="linklike" onClick={logout}>
                  Salir
                </button>
              </>
            ) : (
              <Link to="/login" className="action-link">
                Ingresar
              </Link>
            )}
            <Link to="/cart" className="cart-link">
              Carrito{cartCount > 0 && <span className="cart-count">{cartCount}</span>}
            </Link>
          </div>
        </div>
      </header>

      <main className="page">
        <Routes>
          <Route path="/" element={<Home />} />
          <Route path="/catalogo" element={<Catalog />} />
          <Route path="/libro/:id" element={<BookDetail />} />
          <Route path="/login" element={<Login />} />
          <Route path="/cart" element={<Cart />} />
          <Route path="/profile" element={<RequireAuth><Profile /></RequireAuth>} />
          <Route path="/admin" element={<RequirePanel><Admin /></RequirePanel>} />
          {/* A path that matches nothing used to render a blank page inside the
              chrome, which reads as a broken build rather than a typo. */}
          <Route path="*" element={<NotFound />} />
        </Routes>
      </main>

      <footer className="footer">
        <div className="footer-brand">DVBS.</div>
        <p className="footer-note">
          DVBS — librería independiente de libros nuevos y usados.
        </p>
        <nav className="footer-nav">
          <Link to="/catalogo">Catálogo</Link>
          <Link to="/cart">Carrito</Link>
          <Link to="/profile">Cuenta</Link>
          <Link to="/admin">Administración</Link>
        </nav>
      </footer>
    </div>
  )
}