import { useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { api } from '../api/client.js'
import { useAuth } from '../auth/AuthContext.jsx'

const TABS = ['Ingresar', 'Crear cuenta', 'Recuperar']

export default function Login() {
  const navigate = useNavigate()
  const location = useLocation()
  const { setUser, isAuthenticated } = useAuth()
  // Where the guard bounced the visitor from, so a successful login returns them
  // there instead of dumping them on the home page.
  const from = location.state?.from
  const [tab, setTab] = useState(TABS[0])
  const [loginData, setLoginData] = useState({ email: '', password: '' })
  const [registerData, setRegisterData] = useState({ first_name: '', last_name: '', email: '', password: '' })
  const [resetEmail, setResetEmail] = useState('')
  const [resetData, setResetData] = useState({ token: '', new_password: '' })
  const [codeSent, setCodeSent] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  async function handleLogin(e) {
    e.preventDefault()
    setError('')
    setNotice('')
    const res = await api.post('/auth/login', loginData)
    if (res.ok) {
      // The session is already established by the cookie the server just set;
      // there is no token to keep. Pushing the user into the context re-renders
      // the topbar, which is what the window.location.reload() here used to do
      // by reloading the whole page.
      setUser(res.data.user)
      navigate(from || '/')
    } else {
      setError(res.data?.error || `Error ${res.status}`)
    }
  }

  async function handleRegister(e) {
    e.preventDefault()
    setError('')
    setNotice('')
    const res = await api.post('/auth/register', registerData)
    if (res.ok) {
      setTab(TABS[0])
      setNotice(`Cuenta creada para ${res.data.email} (${res.data.role}). Ya puedes iniciar sesión.`)
    } else {
      setError(res.data?.error || `Error ${res.status}`)
    }
  }

  async function handleRequestCode(e) {
    e.preventDefault()
    setError('')
    setNotice('')
    const res = await api.post('/auth/forgot-password', { email: resetEmail })
    if (res.ok) {
      setCodeSent(true)
      setNotice('Si el correo existe, te hemos enviado un correo con el token de recuperación.')
    } else {
      setError(res.data?.error || `Error ${res.status}`)
    }
  }

  async function handleReset(e) {
    e.preventDefault()
    setError('')
    setNotice('')
    const res = await api.post('/auth/reset-password', {
      token: resetData.token,
      new_password: resetData.new_password,
    })
    if (res.ok) {
      setCodeSent(false)
      setResetData({ token: '', new_password: '' })
      setTab(TABS[0])
      setNotice('Contraseña actualizada. Inicia sesión con la nueva.')
    } else {
      setError(res.data?.error || `Error ${res.status}`)
    }
  }

  return (
    <div className="auth">
      <aside className="auth-side">
        <p className="kicker">Librería DVBS</p>
        <h2 className="auth-claim">
          Cada libro,
          <br />
          una excusa
          <br />
          para leer despacio.
        </h2>
        <p className="auth-note">
          Miembros de la comunidad acceden a su buzón, a sus pedidos y a sus reseñas desde un solo lugar.
        </p>
      </aside>

      <section className="card auth-card">
        <div className="tabs-bar auth-tabs">
          {TABS.map((t) => (
            <button key={t} className={t === tab ? 'tab on' : 'tab'} onClick={() => { setTab(t); setError('') }}>
              {t}
            </button>
          ))}
        </div>

        {tab === 'Ingresar' ? (
          <form className="form" onSubmit={handleLogin}>
            <div className="field">
              <label>Correo electrónico</label>
              <input type="email" value={loginData.email}
                onChange={(e) => setLoginData({ ...loginData, email: e.target.value })} />
            </div>
            <div className="field">
              <label>Contraseña</label>
              <input type="password" value={loginData.password}
                onChange={(e) => setLoginData({ ...loginData, password: e.target.value })} />
            </div>
            <button className="btn btn-solid btn-wide">Iniciar sesión</button>
          </form>
        ) : tab === 'Crear cuenta' ? (
          <form className="form" onSubmit={handleRegister}>
            <div className="field-row">
              <div className="field">
                <label>Nombre</label>
                <input value={registerData.first_name}
                  onChange={(e) => setRegisterData({ ...registerData, first_name: e.target.value })} />
              </div>
              <div className="field">
                <label>Apellido</label>
                <input value={registerData.last_name}
                  onChange={(e) => setRegisterData({ ...registerData, last_name: e.target.value })} />
              </div>
            </div>
            <div className="field">
              <label>Correo electrónico</label>
              <input type="email" value={registerData.email}
                onChange={(e) => setRegisterData({ ...registerData, email: e.target.value })} />
            </div>
            <div className="field">
              <label>Contraseña</label>
              <input type="password" value={registerData.password}
                onChange={(e) => setRegisterData({ ...registerData, password: e.target.value })} />
            </div>
            <button className="btn btn-solid btn-wide">Crear cuenta</button>
        <p className="muted center">
          Al crear tu cuenta aceptas los términos de la librería.
        </p>
          </form>
        ) : (
          <div className="form">
            {!codeSent ? (
              <form className="form" onSubmit={handleRequestCode}>
                <div className="field">
                  <label>Correo electrónico de tu cuenta</label>
                  <input type="email" value={resetEmail}
                    onChange={(e) => setResetEmail(e.target.value)} />
                </div>
                <button className="btn btn-solid btn-wide">Enviar código</button>
              </form>
            ) : (
              <form className="form" onSubmit={handleReset}>
                <p className="muted">
                  Te enviamos un token de recuperación a tu correo. Pégalo aquí para
                  cambiar la contraseña.
                </p>
                <div className="field">
                  <label>Token de recuperación</label>
                  <input value={resetData.token}
                    onChange={(e) => setResetData({ ...resetData, token: e.target.value.trim() })} />
                </div>
                <div className="field">
                  <label>Nueva contraseña</label>
                  <input type="password" value={resetData.new_password}
                    onChange={(e) => setResetData({ ...resetData, new_password: e.target.value })} />
                </div>
                <button className="btn btn-solid btn-wide">Cambiar contraseña</button>
                <button type="button" className="linklike" onClick={() => setCodeSent(false)}>
                  Enviar otro código
                </button>
              </form>
            )}
          </div>
        )}

        {error && <p className="error">{error}</p>}
        {notice && <p className="ok">{notice}</p>}
      </section>
    </div>
  )
}