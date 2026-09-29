import { useEffect, useMemo, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, ensureCSRF, readCSRF, CSRF_HEADER, getUser } from '../api/client.js'
import { addToCart } from '../lib/cart.js'
import {
  Cover,
  Stars,
  money,
  ratingFor,
  publisherFor,
  yearFor,
  synopsisFor,
} from '../lib/books.jsx'

const FORMATOS = ['Tapa dura', 'Tapa blanda', 'eBook']
const TABS = ['Descripción', 'Detalles', 'Reseñas']

export default function BookDetail() {
  const { id } = useParams()
  const navigate = useNavigate()
  const [book, setBook] = useState(null)
  const [related, setRelated] = useState([])
  const [formato, setFormato] = useState(FORMATOS[0])
  const [tab, setTab] = useState(TABS[0])
  const [notice, setNotice] = useState('')
  const [reviewForm, setReviewForm] = useState({ rating: 5, comment: '' })
  const [reviewImage, setReviewImage] = useState(null)
  const [reviewMsg, setReviewMsg] = useState(null)
  const user = getUser()

  useEffect(() => {
    setBook(null)
    setNotice('')
    setTab(TABS[0])
    api.get(`/books/${id}`).then((res) => {
      if (res.ok) {
        setBook(res.data)
        api.get('/books').then((all) => {
          const list = (all.data || []).filter(
            (b) => b.id !== res.data.id && b.author === res.data.author,
          )
          const others = (all.data || []).filter(
            (b) => b.id !== res.data.id && b.author !== res.data.author && b.url_cover_image,
          )
          setRelated([...list, ...others].slice(0, 8))
        })
      } else {
        navigate('/catalogo')
      }
    })
  }, [id])

  const reviews = book?.reviews || []
  const rating = ratingFor(reviews)

  const dist = useMemo(() => {
    const counts = [0, 0, 0, 0, 0]
    reviews.forEach((r) => {
      const idx = Math.min(4, Math.max(0, Math.round(r.rating) - 1))
      counts[idx]++
    })
    return counts
      .map((count, i) => ({ stars: 5 - i, count, pct: reviews.length ? (count / reviews.length) * 100 : 0 }))
      .reverse()
  }, [reviews])

  if (!book) return <div className="page-loading">Cargando ficha…</div>

  function buy(goToCart) {
    // The cart carries identity and quantity only. The price is read back from
    // the catalogue at render and recomputed by the server at checkout.
    addToCart(book.id)
    if (goToCart) navigate('/cart')
    else setNotice(`“${book.title}” (${formato}) añadido al carrito.`)
  }

  async function uploadReviewImage(e) {
    const file = e.target.files?.[0]
    if (!file) return
    // The allowlist mirrors the server's. It is a courtesy check that saves a
    // round trip; the server decides, and a file that lies about its type is
    // rejected there by decoding it rather than reading this header.
    // Must match the server's allowlist exactly. A format the browser offers
    // and the server refuses is a 415 the customer only discovers after
    // choosing the file.
    const allowed = ['image/jpeg', 'image/png', 'image/gif']
    if (!allowed.includes(file.type)) {
      setReviewMsg({ kind: 'error', text: 'Solo se permiten imágenes JPG, PNG, WEBP o GIF.' })
      return
    }
    const body = new FormData()
    body.append('image', file)
    // This upload is a multipart form, so it cannot go through the JSON helper.
    // It still needs the session cookie and the CSRF header, which is why it
    // asks for the token the same way the helper does.
    await ensureCSRF()
    const csrf = readCSRF()
    const res = await fetch('/uploads', {
      method: 'POST',
      credentials: 'include',
      headers: csrf ? { [CSRF_HEADER]: csrf } : {},
      body,
    })
    const data = await res.json().catch(() => ({}))
    if (res.ok) {
      setReviewImage(data.url)
      setReviewMsg({ kind: 'ok', text: 'Imagen adjuntada a la reseña.' })
    } else {
      setReviewMsg({ kind: 'error', text: data.error || `No se pudo subir la imagen (HTTP ${res.status}).` })
    }
  }

  async function submitReview(e) {
    e.preventDefault()
    setReviewMsg(null)
    if (!user) {
      navigate('/login')
      return
    }
    // No user_id: the server takes the author from the session, and sending
    // the field only invites the question of whose value should win.
    const res = await api.post(`/books/${book.id}/reviews`, {
      rating: Number(reviewForm.rating),
      comment: reviewForm.comment,
      image_url: reviewImage || '',
    })
    if (res.ok) {
      setReviewMsg({ kind: 'ok', text: '¡Gracias por tu reseña!' })
      setReviewForm({ rating: 5, comment: '' })
      setReviewImage(null)
      api.get(`/books/${id}`).then((r) => r.ok && setBook(r.data))
    } else {
      setReviewMsg({ kind: 'error', text: res.data?.error || `Error ${res.status}` })
    }
  }

  return (
    <>
      <nav className="crumbs">
        <Link to="/">Inicio</Link> / <Link to="/catalogo">Catálogo</Link> /{' '}
        <span>{book.title}</span>
      </nav>

      <section className="detail">
        <div className="detail-figure">
          <div className="spine-shape" />
          {/* The cover URL is data, not markup: it goes into an <img src>, so
              it is fetched and decoded as an image and never parsed as a
              document. A javascript: or data:text/html value cannot execute
              from an image context. */}
          <Cover book={book} className="cover-detail" />
        </div>

        <div className="detail-info">
          <h1 className="detail-title">{book.title}</h1>
          <p className="detail-author">
            de <a onClick={() => navigate(`/catalogo?q=${encodeURIComponent(book.author)}`)}>{book.author}</a>
          </p>

          <p className="detail-rating">
            {rating !== null ? (
              <>
                <Stars value={rating} /> <b>{rating.toFixed(1)}</b>
                <span className="muted"> · {reviews.length} reseñas</span>
              </>
            ) : (
              <span className="muted">Sin reseñas todavía — sé el primero.</span>
            )}
          </p>

          <p className="detail-price">
            <b>{money(book.price_cents)}</b>
          </p>

          <div className="format-picker">
            {FORMATOS.map((f) => (
              <button key={f} className={f === formato ? 'format on' : 'format'} onClick={() => setFormato(f)}>
                {f}
              </button>
            ))}
          </div>

          <div className="hero-cta">
            <button className="btn btn-solid" onClick={() => buy(false)}>
              Añadir al carrito
            </button>
            <button className="btn btn-line" onClick={() => buy(true)}>
              Comprar ahora
            </button>
          </div>
          {notice && <p className="ok">{notice}</p>}
        </div>
      </section>

      <section className="tabs-section">
        <div className="tabs-bar">
          {TABS.map((t) => (
            <button key={t} className={t === tab ? 'tab on' : 'tab'} onClick={() => setTab(t)}>
              {t}
              {t === 'Reseñas' && ` (${reviews.length})`}
            </button>
          ))}
        </div>

        <div className="tab-body">
          {tab === 'Descripción' && (
            <div className="prose">
              <p>{synopsisFor(book)}</p>
            </div>
          )}

          {tab === 'Detalles' && (
            <dl className="specs">
              <div>
                <dt>ISBN</dt>
                <dd>{book.isbn}</dd>
              </div>
              <div>
                <dt>Páginas</dt>
                <dd>{book.pages}</dd>
              </div>
              <div>
                <dt>Editorial</dt>
                <dd>{publisherFor(book)}</dd>
              </div>
              <div>
                <dt>Año</dt>
                <dd>{yearFor(book)}</dd>
              </div>
              <div>
                <dt>Idioma</dt>
                <dd>Español</dd>
              </div>
              <div>
                <dt>Formato</dt>
                <dd>{formato}</dd>
              </div>
              <div>
                <dt>Disponibilidad</dt>
                <dd>{book.stock > 0 ? `${book.stock} ejemplares` : 'Agotado'}</dd>
              </div>
            </dl>
          )}

          {tab === 'Reseñas' && (
            <div className="reviews-wrap">
              <div className="rating-dist">
                <p className="dist-average">
                  {rating !== null ? rating.toFixed(1) : '—'}
                  <small>/5</small>
                </p>
                {dist.map((d) => (
                  <div key={d.stars} className="dist-row">
                    <span className="dist-stars">{d.stars}★</span>
                    <div className="dist-track">
                      <div className="dist-fill" style={{ width: `${d.pct}%` }} />
                    </div>
                    <span className="dist-count">{d.count}</span>
                  </div>
                ))}
              </div>

              <ul className="review-list">
                {reviews.length === 0 && <li className="muted">Aún no hay reseñas de este título.</li>}
                {reviews.map((r) => (
                  <li key={r.id} className="review-item">
                    <header>
                      <b>Lector #{r.user_id}</b>
                      <Stars value={r.rating} />
                      <time>{new Date(r.created_at).toLocaleDateString()}</time>
                    </header>
                    <div className="review-body">{r.comment}</div>
                    {r.image_url && (
                      <div className="review-image">
                        {/* <img>, not <object>. An <object> loads the file as a
                            document in our own origin, so a payload inside it ran
                            with our cookies and storage. The server no longer stores
                            anything but re-encoded pixels, and this is the second
                            half: an image context has nowhere for a script to run. */}
                        <img
                          src={r.image_url}
                          width="120"
                          height="120"
                          alt="Imagen adjunta de la reseña"
                          loading="lazy"
                        />
                        <a href={r.image_url} target="_blank" rel="noreferrer" className="review-image-link">
                          Ver imagen adjunta
                        </a>
                      </div>
                    )}
                  </li>
                ))}
              </ul>

              <form className="form review-form" onSubmit={submitReview}>
                <h3>Escribe tu reseña</h3>
                <div className="field">
                  <label htmlFor="rating">Puntuación</label>
                  <select
                    id="rating"
                    value={reviewForm.rating}
                    onChange={(e) => setReviewForm({ ...reviewForm, rating: e.target.value })}
                  >
                    {[5, 4, 3, 2, 1].map((n) => (
                      <option key={n} value={n}>{n} estrella{n > 1 ? 's' : ''}</option>
                    ))}
                  </select>
                </div>
                <div className="field">
                  <label htmlFor="comment">Comentario</label>
                  <textarea
                    id="comment"
                    rows={4}
                    value={reviewForm.comment}
                    onChange={(e) => setReviewForm({ ...reviewForm, comment: e.target.value })}
                    placeholder="¿Qué te pareció el libro?"
                  />
                </div>
                <div className="field">
                  <label htmlFor="review-image">Imagen (JPG, PNG o WEBP)</label>
                  <input
                    id="review-image"
                    type="file"
                    // The same three formats the server will store, and it
                    // re-encodes all of them. WebP used to be listed here and
                    // rejected on arrival: the standard library cannot decode
                    // it, so accepting it would have meant storing the client's
                    // bytes without ever taking them apart.
                    accept="image/jpeg,image/png,image/gif"
                    onChange={uploadReviewImage}
                  />
                  {reviewImage && <span className="muted">Imagen lista: {reviewImage}</span>}
                </div>
                {reviewMsg && <p className={reviewMsg.kind === 'ok' ? 'ok' : 'error'}>{reviewMsg.text}</p>}
                <button className="btn btn-solid" type="submit">Publicar reseña</button>
                {!user && <p className="muted">Inicia sesión para publicar.</p>}
              </form>
            </div>
          )}
        </div>
      </section>

      {related.length > 0 && (
        <section className="related">
          <h2>Del mismo autor y afines</h2>
          <div className="carousel">
            {related.map((b) => (
              <article key={b.id} className="rcard" onClick={() => navigate(`/libro/${b.id}`)}>
                <Cover book={b} />
                <p className="rcard-title">{b.title}</p>
                <p className="rcard-price">{money(b.price_cents)}</p>
              </article>
            ))}
          </div>
        </section>
      )}
    </>
  )
}