import { MoonIcon, SunIcon } from '@heroicons/react/24/outline';
import React from 'react';

const ThemeSwitcher = () => {
  // Get initial theme from localStorage or default to light
  const [theme, setTheme] = React.useState('light');

  // Initialize theme from localStorage after component mounts
  React.useEffect(() => {
    const savedTheme = localStorage.getItem('theme');
    if (savedTheme && (savedTheme === 'light' || savedTheme === 'dark')) {
      setTheme(savedTheme);
    }
  }, []);

  const toggleTheme = () => {
    const newTheme = theme === 'dark' ? 'light' : 'dark';
    setTheme(newTheme);
    localStorage.setItem('theme', newTheme);
  };

  // "listen" for changes to apply them to the HTML tag
  React.useEffect(() => {
    document.querySelector('html')?.setAttribute('data-theme', theme);
  }, [theme]);

  return (
    <div className="tooltip tooltip-bottom" data-tip={`Switch to ${theme === 'dark' ? 'light' : 'dark'} theme`}>
        <label className="swap swap-rotate">
          <input 
            onClick={toggleTheme} 
            type="checkbox" 
            checked={theme === 'dark'}
          />
          <MoonIcon className="w-8 swap-on" />
          <SunIcon className="w-8 swap-off" />
        </label>
    </div>
  );
};

export default ThemeSwitcher;
