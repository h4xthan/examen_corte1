import { useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api, getUser } from '../api/client.js'
import { getCart, clearCart, saveCart, setQuantity as saveQuantity, removeFromCart, MAX_QTY } from '../lib/cart.js'
import { Cover, money } from '../lib/books.jsx'

export default function Cart() {
  const navigate = useNavigate()
  const user = getUser()
  const [cart, setCartState] = useState(() => getCart())
  const [coupon, setCoupon] = useState('')
  const [quote, setQuote] = useState(null)
  const [couponMsg, setCouponMsg] = useState(null)
  const [result, setResult] = useState(null)
  const [error, setError] = useState('')
  const [balance, setBalance] = useState(null)
  const [catalog, setCatalog] = useState([])
  const [busy, setBusy] = useState(false)

  const refreshBalance = () => {
    if (!user) return
    api.get('/users/me').then((res) => {
      if (res.ok) setBalance(res.data.balance_cents)
    })
  }

  useEffect(() => {
    // The cart lives in localStorage, so a change in another tab has to be
    // adopted here too or the two disagree about what is being bought.
    const onChange = () => setCartState(getCart())
    window.addEventListener('cart-changed', onChange)
    return () => window.removeEventListener('cart-changed', onChange)
  }, [])

  useEffect(() => {
    if (!user) return
    // Only an array becomes the catalogue. On a failure the body is an error
    // object, and handing that to .find() below would throw a TypeError and
    // blank the page instead of showing an empty cart.
    api.get('/books').then((res) => setCatalog(res.ok && Array.isArray(res.data) ? res.data : []))
    refreshBalance()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [user?.id])

  // Prices come from the catalogue, not from whatever the cart stored. A cart is
  // client state and a price is a fact about the shop; showing the stored copy
  // would mean the summary can disagree with the charge.
  const priced = cart.map((item) => {
    const book = catalog.find((b) => b.id === item.id)
    return {
      ...item,
      book,
      priceCents: book ? book.price_cents : null,
      known: Boolean(book),
    }
  })
  const unknown = priced.filter((i) => !i.known)

  // A display estimate only. The server recomputes the whole figure from the
  // catalogue inside the checkout transaction, and the order it writes is the
  // one that counts; this number is here so the page can show a total at all.
  const subtotal = priced.reduce((sum, i) => sum + (i.priceCents ?? 0) * i.quantity, 0)
  const discount = quote && quote.discount_cents > 0 && quote.subtotal_cents === subtotal
    ? quote.discount_cents
    : 0
  const total = Math.max(0, subtotal - discount)

  async function applyCoupon(e) {
    e.preventDefault()
    setCouponMsg(null)
    setQuote(null)
    const code = coupon.trim().toUpperCase()
    if (!code) return
    if (subtotal <= 0) {
      setCouponMsg({ kind: 'error', text: 'Añade algo al carrito antes de aplicar un cupón.' })
      return
    }

    // A quote, not a redemption. The server checks the code and reports what it
    // would be worth; nothing is spent until checkout, and nothing is held in
    // between.
    const res = await api.post('/coupons/code', { code, subtotal_cents: subtotal })
    if (res.ok && res.data.status === 'active') {
      setQuote({ ...res.data, subtotal_cents: subtotal })
      setCouponMsg({
        kind: 'ok',
        text: `Cupón ${res.data.code}: −${res.data.discount_percent}% (−${money(res.data.discount_cents)})`,
      })
    } else if (res.ok) {
      setCouponMsg({ kind: 'error', text: 'Ese código no está disponible.' })
    } else {
      setCouponMsg({ kind: 'error', text: res.data?.error || 'No se pudo consultar el cupón.' })
    }
  }

  function removeCoupon() {
    setQuote(null)
    setCouponMsg(null)
    setCoupon('')
  }

  function removeItem(index) {
    removeFromCart(cart[index].id)
    const next = cart.filter((_, i) => i !== index)
    setCartState(next)
    // The quote was for a different subtotal, so it cannot be carried over.
    setQuote(null)
    setCouponMsg(null)
  }

  function setQuantity(index, quantity) {
    // The clamp and the 1..10 range live in the cart module, so the header, the
    // detail page and this page cannot disagree about what a quantity is.
    saveQuantity(cart[index].id, quantity)
    setCartState(getCart())
    // The quote was for a different subtotal, so it cannot be carried over.
    setQuote(null)
    setCouponMsg(null)
  }

  async function checkout() {
    setError('')
    if (!user) {
      navigate('/login')
      return
    }
    if (unknown.length > 0) {
      setError('Hay títulos en el carrito que ya no están en el catálogo. Quítalos para continuar.')
      return
    }
    setBusy(true)
    try {
      // Only what is needed to say what should be bought. No user_id, no total,
      // no unit prices: the server has the session, the catalogue and the
      // arithmetic, and a body that could contradict any of them would just be
      // a field waiting to be trusted.
      const res = await api.post('/checkout', {
        items: priced.map((i) => ({ book_id: i.id, quantity: i.quantity })),
        coupon_code: quote?.code || '',
      })
      if (res.ok) {
        setResult(res.data)
        clearCart()
        setCartState([])
        setQuote(null)
        refreshBalance()
      } else {
        setError(res.data?.error || `No se pudo completar la compra (HTTP ${res.status}).`)
        refreshBalance()
      }
    } finally {
      setBusy(false)
    }
  }

  if (result) return <Confirmation result={result} />

  if (cart.length === 0) {
    return (
      <div className="empty-cart">
        <p className="kicker">Tu carrito</p>
        <h2 className="auth-claim">Aún no hay nada<br />que leer aquí.</h2>
        <p className="muted">Explora el catálogo y añade tu próxima lectura.</p>
        <Link to="/catalogo" className="btn btn-solid">Ir al catálogo</Link>
      </div>
    )
  }

  return (
    <div className="cart-layout">
      <section className="cart-items">
        <header className="section-head">
          <h2>Tu carrito</h2>
          <span className="count-note">{cart.length} artículos</span>
        </header>

        {priced.map((item, i) => (
          <article key={item.id} className="card cart-item">
            <div className="cart-thumb" onClick={() => navigate(`/libro/${item.id}`)}>
              <Cover book={{ ...item.book, title: item.title }} />
            </div>
            <div className="cart-item-info">
              <h3>{item.title}</h3>
              {item.book?.author && <p className="muted">{item.book.author}</p>}
              {item.priceCents === null ? (
                <p className="error">Este título ya no está disponible.</p>
              ) : (
                <p className="cart-unit">{money(item.priceCents)} · unidad</p>
              )}
              <div className="cart-qty">
                <button
                  type="button"
                  className="btn btn-line btn-small"
                  onClick={() => setQuantity(i, Math.max(1, item.quantity - 1))}
                  disabled={item.quantity <= 1}
                  aria-label={`Quitar una unidad de ${item.title}`}
                >
                  −
                </button>
                <span>{item.quantity}</span>
                <button
                  type="button"
                  className="btn btn-line btn-small"
                  onClick={() => setQuantity(i, item.quantity + 1)}
                  // The server rejects more than 10; the limit is repeated here
                  // so the mistake is caught before the round trip.
                  disabled={item.quantity >= MAX_QTY}
                  aria-label={`Añadir una unidad de ${item.title}`}
                >
                  +
                </button>
              </div>
            </div>
            <button className="linklike cart-remove" onClick={() => removeItem(i)}>
              Quitar
            </button>
          </article>
        ))}
      </section>

      <aside className="summary">
        <h3>Resumen del pedido</h3>

        <form className="coupon-row" onSubmit={applyCoupon}>
          <input placeholder="Código de cupón" value={coupon}
            onChange={(e) => setCoupon(e.target.value)} />
          <button className="btn btn-line btn-small" disabled={busy}>Aplicar</button>
        </form>
        {couponMsg && (
          <p className={couponMsg.kind === 'ok' ? 'ok' : 'error'}>
            {couponMsg.text}
            {quote && (
              <button type="button" className="linklike cart-remove" onClick={removeCoupon}>
                Quitar
              </button>
            )}
          </p>
        )}

        <dl className="summary-lines">
          <div>
            <dt>Subtotal</dt>
            <dd>{money(subtotal)}</dd>
          </div>
          {discount > 0 && (
            <div>
              <dt>Descuento</dt>
              <dd>−{money(discount)}</dd>
            </div>
          )}
          <div className="summary-total">
            <dt>Total</dt>
            <dd>{money(total)}</dd>
          </div>
        </dl>

        {error && <p className="error">{error}</p>}
        {!user && <p className="hint">Inicia sesión para completar la compra.</p>}
        {balance !== null && (
          <dl className="summary-lines">
            <div className="summary-total">
              <dt>Crédito disponible</dt>
              <dd>{money(balance)}</dd>
            </div>
          </dl>
        )}
        {balance !== null && total > balance && (
          <p className="hint">El crédito disponible no cubre este pedido.</p>
        )}
        <button
          className="btn btn-solid btn-wide"
          onClick={checkout}
          disabled={!user || busy || unknown.length > 0}
        >
          {busy ? 'Procesando…' : 'Finalizar compra'}
        </button>
        <p className="hint">El importe final lo calcula el servidor al confirmar.</p>
      </aside>
    </div>
  )
}

function Confirmation({ result }) {
  const order = result.order || {}
  return (
    <div className="confirm">
      <div className="confirm-mark">✔</div>
      <p className="kicker">Pedido registrado</p>
      <h2 className="confirm-title">¡Gracias por tu compra!</h2>

      <div className="card confirm-card">
        <dl className="specs confirm-specs">
          <div>
            <dt>Número de pedido</dt>
            <dd>#{order.id}</dd>
          </div>
          <div>
            <dt>Estado</dt>
            <dd>{order.status}</dd>
          </div>
          <div>
            <dt>Subtotal</dt>
            <dd>{money(order.subtotal_cents)}</dd>
          </div>
          {order.discount_cents > 0 && (
            <div>
              <dt>Descuento</dt>
              <dd>−{money(order.discount_cents)}</dd>
            </div>
          )}
          <div>
            <dt>Total pagado</dt>
            <dd>{money(order.total_cents)}</dd>
          </div>
          <div>
            <dt>Cupón</dt>
            <dd>{result.coupon ? result.coupon.code : 'Sin cupón'}</dd>
          </div>
          <div>
            <dt>Fecha</dt>
            <dd>{new Date(order.created_at).toLocaleString()}</dd>
          </div>
        </dl>
      </div>

      <div className="hero-cta confirm-cta">
        <Link to="/profile" className="btn btn-solid">Ver mis pedidos</Link>
        <Link to="/catalogo" className="btn btn-line">Seguir comprando</Link>
      </div>
    </div>
  )
}
