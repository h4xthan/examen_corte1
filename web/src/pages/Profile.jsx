import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../api/client.js'
import { useAuth } from '../auth/AuthContext.jsx'
import { money } from '../lib/books.jsx'

export default function Profile() {
  const { user } = useAuth()
  const [orders, setOrders] = useState([])
  const [cards, setCards] = useState([])
  const [addresses, setAddresses] = useState([])
  const [cardForm, setCardForm] = useState({ card_number: '', expiry_month: '', expiry_year: '' })
  const [addrForm, setAddrForm] = useState({ street: '', city: '', zip: '' })
  const [loaded, setLoaded] = useState(false)
  const [cardMsg, setCardMsg] = useState('')

  async function loadAll() {
    if (!user) return
    // GET /orders lists every order in the shop and is admin-only (#5), so the
    // history comes from /orders/me, whose query is already scoped to the
    // session. The filter below is belt and braces, not the mechanism.
    const [ordersRes, cardsRes, addrRes] = await Promise.all([
      api.get('/orders/me'),
      api.get(`/users/${user.id}/payment-methods`),
      api.get(`/users/${user.id}/addresses`),
    ])
    // The filter is redundant with the server-side scoping and stays as a
    // reminder that the shape of this response is not a detail.
    setOrders((ordersRes.ok ? ordersRes.data || [] : []).filter((o) => o.user_id === user.id).reverse())
    setCards(cardsRes.data || [])
    setAddresses(addrRes.data || [])
    setLoaded(true)
  }

  useEffect(() => {
    loadAll()
  }, [])

  if (!user) {
    return (
      <div className="empty-cart">
        <p className="kicker">Tu cuenta</p>
        <h2 className="auth-claim">Inicia sesión<br />para ver tu perfil.</h2>
        {/* This was href="#/login", HashRouter syntax, in an app that uses
            BrowserRouter: the click reloaded the page and bounced the visitor
            back here. */}
        <Link to="/login" className="btn btn-solid">Ir a ingresar</Link>
      </div>
    )
  }

  async function addCard(e) {
    e.preventDefault()
    setCardMsg('')
    // No CVV, and no card holder. The server keeps brand, last four digits and
    // expiry, and rejects an unknown field, so a stray cvv here would be a 400
    // rather than a stored secret. PCI-DSS forbids keeping a CVV after
    // authorisation, so it is not stored anywhere, not even hashed.
    const res = await api.post(`/users/${user.id}/payment-methods`, {
      card_number: cardForm.card_number.replace(/[\s-]/g, ''),
      expiry_month: Number(cardForm.expiry_month),
      expiry_year: Number(cardForm.expiry_year),
    })
    if (res.ok) {
      setCardForm({ card_number: '', expiry_month: '', expiry_year: '' })
      loadAll()
    } else {
      setCardMsg(res.data?.error || `No se pudo guardar la tarjeta (HTTP ${res.status}).`)
    }
  }

  function removeCard(id) {
    api.del(`/payment-methods/${id}`).then(() => loadAll())
  }

  async function addAddress(e) {
    e.preventDefault()
    const res = await api.post(`/users/${user.id}/addresses`, addrForm)
    if (res.ok) {
      setAddrForm({ street: '', city: '', zip: '' })
      loadAll()
    }
  }

  const initials = `${user.first_name?.[0] || ''}${user.last_name?.[0] || ''}`.toUpperCase()

  return (
    <div>
      <header className="profile-head">
        <div className="avatar">{initials}</div>
        <div>
          <h1 className="profile-name">
            {user.first_name} {user.last_name}
          </h1>
          <p className="muted">
            {user.email}
          </p>
        </div>
      </header>

      <section className="section">
        <header className="section-head">
          <h2>Mis pedidos</h2>
          <span className="count-note">{orders.length} pedidos</span>
        </header>
        {loaded && orders.length === 0 && (
          <p className="muted">
            Todavía no has hecho ningún pedido.{' '}
            <Link to="/catalogo" className="section-link">
              Explorar el catálogo →
            </Link>
          </p>
        )}
        <div className="orders-list">
          {orders.map((o) => (
            <article key={o.id} className="card order-row">
              <div className="order-id">#{o.id}</div>
              <div className="order-meta">
                <time>{new Date(o.created_at).toLocaleDateString()}</time>
                <span className={`badge-status s-${o.status}`}>
                  {o.status === 'delivered' ? 'Entregado' : o.status === 'shipped' ? 'Enviado' : 'Pendiente'}
                </span>
              </div>
              <div className="order-total">{money(o.total_cents)}</div>
            </article>
          ))}
        </div>
      </section>

      <section className="profile-grid">
        <article className="card">
          <header className="card-head">
            <h2>Métodos de pago</h2>
            <span className="count-note">{cards.length}</span>
          </header>
          <ul className="plain-list">
            {cards.length === 0 && <li className="muted">Aún no has guardado ninguna tarjeta.</li>}
            {/* Only the last four digits exist. The full number and the CVV are
                not in the response because they are not in the database, so
                there is nothing to mask here. */}
            {cards.map((c) => (
              <li key={c.id}>
                💳 <code>•••• {c.last4}</code> · {c.brand} · exp {String(c.expiry_month).padStart(2, '0')}/{c.expiry_year}
                <button className="linklike cart-remove" onClick={() => removeCard(c.id)}>Quitar</button>
              </li>
            ))}
          </ul>
          <form onSubmit={addCard} className="form">
            <input placeholder="Número de tarjeta" inputMode="numeric" autoComplete="off"
              value={cardForm.card_number}
              onChange={(e) => setCardForm({ ...cardForm, card_number: e.target.value })} />
            <div className="field-row">
              <input placeholder="Mes (MM)" inputMode="numeric" value={cardForm.expiry_month}
                onChange={(e) => setCardForm({ ...cardForm, expiry_month: e.target.value })} />
              <input placeholder="Año (AAAA)" inputMode="numeric" value={cardForm.expiry_year}
                onChange={(e) => setCardForm({ ...cardForm, expiry_year: e.target.value })} />
            </div>
            {cardMsg && <p className="error">{cardMsg}</p>}
            <button className="btn btn-line btn-small">Guardar tarjeta</button>
          </form>
        </article>

        <article className="card">
          <header className="card-head">
            <h2>Direcciones</h2>
            <span className="count-note">{addresses.length}</span>
          </header>
          <ul className="plain-list">
            {addresses.length === 0 && <li className="muted">Aún no tienes direcciones guardadas.</li>}
            {addresses.map((a) => (
              <li key={a.id}>
                📍 {a.street}, {a.city}, {a.zip}
              </li>
            ))}
          </ul>
          <form onSubmit={addAddress} className="form">
            <input placeholder="Calle y número" value={addrForm.street}
              onChange={(e) => setAddrForm({ ...addrForm, street: e.target.value })} />
            <div className="field-row">
              <input placeholder="Ciudad" value={addrForm.city}
                onChange={(e) => setAddrForm({ ...addrForm, city: e.target.value })} />
              <input placeholder="Código postal" value={addrForm.zip}
                onChange={(e) => setAddrForm({ ...addrForm, zip: e.target.value })} />
            </div>
            <button className="btn btn-line btn-small">Guardar dirección</button>
          </form>
        </article>
      </section>
    </div>
  )
}