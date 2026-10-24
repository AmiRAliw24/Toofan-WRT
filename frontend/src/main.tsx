import { render } from 'preact';
import './styles.css';
import { App } from './app';
import { applyDir } from './i18n';

applyDir();
render(<App />, document.getElementById('app')!);
